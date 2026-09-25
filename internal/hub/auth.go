package hub

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

const (
	sessionCookie = "session"
	bcryptCost    = 12
	maxNameLen    = 64
	loginFailures = 5
	loginLockout  = 5 * time.Minute
	// elevationTTL is how long a re-authentication unlocks sensitive actions.
	elevationTTL = 10 * time.Minute
)

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is how session and enrollment tokens are stored: a leaked database
// does not yield usable tokens.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// dummyHash keeps failed logins for unknown users as slow as for known ones.
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)
	return h
})

type sessionHandler func(http.ResponseWriter, *http.Request, *store.Session)

func (h *Hub) requireUser(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		s, err := h.store.Session(hashToken(c.Value))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				h.log.Error("session lookup failed", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next(w, r, s)
	}
}

// requireRole only lets users through whose role includes role.
func (h *Hub) requireRole(role string, next sessionHandler) http.HandlerFunc {
	return h.requireUser(func(w http.ResponseWriter, r *http.Request, s *store.Session) {
		if !s.Can(role) {
			writeError(w, http.StatusForbidden, "your role ("+s.Role+") does not allow this")
			return
		}
		next(w, r, s)
	})
}

// requireElevated checks for a recent re-authentication, which actions such as
// opening files or running scripts need. The UI then asks for the password again.
func requireElevated(w http.ResponseWriter, s *store.Session) bool {
	if s.ElevatedUntil >= time.Now().Unix() {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm your password first", "reauth_required": true})
	return false
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"` // TOTP code, when two-factor login is on

	// Passkey replaces username, password and code when signing in or confirming
	// with a passkey.
	Passkey *assertion `json:"passkey"`
}

func validatePassword(pw string) string {
	switch {
	case len(pw) < 8:
		return "password must be at least 8 characters"
	case len(pw) > 72:
		return "password must be at most 72 bytes"
	}
	return ""
}

func (c *credentials) validate() string {
	c.Username = strings.TrimSpace(c.Username)
	if c.Username == "" || utf8.RuneCountInString(c.Username) > maxNameLen {
		return "username must be 1-64 characters"
	}
	return validatePassword(c.Password)
}

type meDTO struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Role          string `json:"role"`
	TOTP          bool   `json:"totp"`
	Passkeys      int    `json:"passkeys"`
	ElevatedUntil int64  `json:"elevated_until"`
}

func (h *Hub) me(u *store.User, elevatedUntil int64) meDTO {
	d := meDTO{ID: u.ID, Username: u.Username, Role: u.Role, TOTP: u.TOTPSecret != "", ElevatedUntil: elevatedUntil}
	if keys, err := h.store.Passkeys(u.ID); err == nil {
		d.Passkeys = len(keys)
	}
	return d
}

func (h *Hub) getSetup(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.CountUsers()
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needed": n == 0})
}

// postSetup creates the first (admin) account. It only works while no user exists.
func (h *Hub) postSetup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if msg := c.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	h.setupMu.Lock()
	defer h.setupMu.Unlock()
	n, err := h.store.CountUsers()
	if err != nil {
		h.internalError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "setup is already complete")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcryptCost)
	if err != nil {
		h.internalError(w, err)
		return
	}
	u, err := h.store.CreateUser(c.Username, string(hash), store.RoleAdmin)
	if err != nil {
		h.internalError(w, err)
		return
	}
	h.log.Info("admin account created", "username", u.Username)
	h.audit(r, u.Username, "setup", nil, "created the admin account")
	h.startSession(w, r, u)
}

func (h *Hub) postLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !h.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again in a few minutes")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if c.Passkey != nil {
		h.loginWithPasskey(w, r, c.Passkey)
		return
	}
	u, err := h.store.UserByName(strings.TrimSpace(c.Username))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.internalError(w, err)
		return
	}
	hash := dummyHash()
	if err == nil {
		hash = []byte(u.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(c.Password)) != nil || err != nil {
		h.limiter.fail(ip)
		h.audit(r, c.Username, "login_failed", nil, "wrong username or password")
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if u.TOTPSecret != "" {
		if strings.TrimSpace(c.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "enter the code from your authenticator app", "totp_required": true})
			return
		}
		if !h.checkTOTP(u.ID, u.TOTPSecret, c.Code) {
			h.limiter.fail(ip)
			h.audit(r, u.Username, "login_failed", nil, "wrong two-factor code")
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid code", "totp_required": true})
			return
		}
	}
	h.limiter.reset(ip)
	h.audit(r, u.Username, "login", nil, "")
	h.startSession(w, r, u)
}

func (h *Hub) startSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	token := randomToken()
	expires := time.Now().Add(sessionTTL)
	if err := h.store.CreateSession(hashToken(token), u.ID, expires); err != nil {
		h.internalError(w, err)
		return
	}
	if err := h.store.SetLastLogin(u.ID, time.Now()); err != nil {
		h.log.Error("recording sign-in time failed", "err", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, h.me(u, 0))
}

func (h *Hub) postLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		hash := hashToken(c.Value)
		if s, err := h.store.Session(hash); err == nil {
			h.audit(r, s.Username, "logout", nil, "")
		}
		h.closeShells(hash)
		if err := h.store.DeleteSession(hash); err != nil {
			h.log.Error("deleting session failed", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) getMe(w http.ResponseWriter, _ *http.Request, s *store.Session) {
	writeJSON(w, http.StatusOK, h.me(&s.User, s.ElevatedUntil))
}

// reauthenticate checks the user's password and, if withCode and two-factor login
// is on, a TOTP code. It is rate limited like logins, since it is effectively one.
func (h *Hub) reauthenticate(w http.ResponseWriter, r *http.Request, s *store.Session, password, code string, withCode bool, action string) bool {
	ip := clientIP(r)
	if !h.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again in a few minutes")
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(password)) != nil {
		h.limiter.fail(ip)
		h.audit(r, s.Username, action+"_failed", nil, "wrong password")
		writeError(w, http.StatusForbidden, "wrong password")
		return false
	}
	if withCode && s.TOTPSecret != "" && !h.checkTOTP(s.ID, s.TOTPSecret, code) {
		h.limiter.fail(ip)
		h.audit(r, s.Username, action+"_failed", nil, "wrong two-factor code")
		writeError(w, http.StatusForbidden, "invalid two-factor code")
		return false
	}
	h.limiter.reset(ip)
	return true
}

// postElevate re-authenticates the session for a few minutes (like sudo), which
// opening a shell requires. A passkey replaces password and code.
func (h *Hub) postElevate(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body credentials
	if !readJSON(w, r, &body) {
		return
	}
	detail := ""
	if body.Passkey != nil {
		name := h.reauthenticateWithPasskey(w, r, s, body.Passkey)
		if name == "" {
			return
		}
		detail = fmt.Sprintf("with passkey %q", name)
	} else if !h.reauthenticate(w, r, s, body.Password, body.Code, true, "reauth") {
		return
	}
	until := time.Now().Add(elevationTTL)
	if err := h.store.ElevateSession(s.TokenHash, until); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "reauth", nil, detail)
	writeJSON(w, http.StatusOK, map[string]int64{"elevated_until": until.Unix()})
}

func (h *Hub) postPassword(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if msg := validatePassword(body.New); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !h.reauthenticate(w, r, s, body.Current, "", false, "password_change") {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcryptCost)
	if err != nil {
		h.internalError(w, err)
		return
	}
	if err := h.store.SetPassword(s.ID, string(hash)); err != nil {
		h.internalError(w, err)
		return
	}
	// Anyone who knew the old password is logged out.
	if err := h.store.DeleteOtherSessions(s.ID, s.TokenHash); err != nil {
		h.log.Error("ending other sessions failed", "err", err)
	}
	h.audit(r, s.Username, "password_changed", nil, "other sessions were logged out")
	w.WriteHeader(http.StatusNoContent)
}

// ---- two-factor login (TOTP, RFC 6238) ----

var totpOpts = totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// postTOTPSetup generates a secret for the user to scan. Nothing is stored until
// postTOTPEnable proves the authenticator app produces valid codes.
func (h *Hub) postTOTPSetup(w http.ResponseWriter, _ *http.Request, s *store.Session) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Lotse", AccountName: s.Username})
	if err != nil {
		h.internalError(w, err)
		return
	}
	img, err := key.Image(240, 240)
	if err != nil {
		h.internalError(w, err)
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": key.Secret(),
		"uri":    key.URL(),
		"qr":     "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	})
}

func (h *Hub) postTOTPEnable(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		Secret   string `json:"secret"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if s.TOTPSecret != "" {
		writeError(w, http.StatusConflict, "two-factor login is already on")
		return
	}
	if !h.reauthenticate(w, r, s, body.Password, "", false, "totp_enable") {
		return
	}
	if !h.checkTOTP(s.ID, body.Secret, body.Code) {
		writeError(w, http.StatusBadRequest, "that code does not match; check the time on your phone and try the next code")
		return
	}
	if err := h.store.SetTOTPSecret(s.ID, body.Secret); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "totp_enabled", nil, "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) postTOTPDisable(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body credentials
	if !readJSON(w, r, &body) {
		return
	}
	if s.TOTPSecret == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !h.reauthenticate(w, r, s, body.Password, body.Code, true, "totp_disable") {
		return
	}
	if err := h.store.SetTOTPSecret(s.ID, ""); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "totp_disabled", nil, "")
	w.WriteHeader(http.StatusNoContent)
}

// checkTOTP accepts the current code or its neighbors (clock skew). A code that
// was already used cannot be replayed within its validity window.
func (h *Hub) checkTOTP(userID int64, secret, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if secret == "" || len(code) != 6 {
		return false
	}
	now := time.Now()
	for _, skew := range []int64{0, -1, 1} {
		t := now.Add(time.Duration(skew) * 30 * time.Second)
		want, err := totp.GenerateCodeCustom(secret, t, totpOpts)
		if err != nil || subtle.ConstantTimeCompare([]byte(want), []byte(code)) != 1 {
			continue
		}
		step := t.Unix() / 30
		h.totpMu.Lock()
		defer h.totpMu.Unlock()
		if step <= h.totpUsed[userID] {
			return false
		}
		h.totpUsed[userID] = step
		return true
	}
	return false
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// loginLimiter locks an IP out for a while after repeated failed logins.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failRecord
}

type failRecord struct {
	count int
	first time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string]*failRecord{}}
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec := l.fails[ip]
	if rec == nil {
		return true
	}
	if time.Since(rec.first) > loginLockout {
		delete(l.fails, ip)
		return true
	}
	return rec.count < loginFailures
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 10000 { // bound memory; stale entries are harmless to drop
		clear(l.fails)
	}
	rec := l.fails[ip]
	if rec == nil {
		rec = &failRecord{first: time.Now()}
		l.fails[ip] = rec
	}
	rec.count++
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
