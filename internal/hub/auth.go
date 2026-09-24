package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

const (
	sessionCookie = "session"
	bcryptCost    = 12
	maxNameLen    = 64
	loginFailures = 5
	loginLockout  = 5 * time.Minute
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

type userHandler func(http.ResponseWriter, *http.Request, *store.User)

func (h *Hub) requireUser(next userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		u, err := h.store.SessionUser(hashToken(c.Value))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				h.log.Error("session lookup failed", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next(w, r, u)
	}
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *credentials) validate() string {
	c.Username = strings.TrimSpace(c.Username)
	switch {
	case c.Username == "" || utf8.RuneCountInString(c.Username) > maxNameLen:
		return "username must be 1-64 characters"
	case len(c.Password) < 8:
		return "password must be at least 8 characters"
	case len(c.Password) > 72:
		return "password must be at most 72 bytes"
	}
	return ""
}

type userDTO struct {
	Username string `json:"username"`
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
	u, err := h.store.CreateUser(c.Username, string(hash))
	if err != nil {
		h.internalError(w, err)
		return
	}
	h.log.Info("admin account created", "username", u.Username)
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
		h.log.Warn("failed login", "username", c.Username, "remote", ip)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	h.limiter.reset(ip)
	h.startSession(w, r, u)
}

func (h *Hub) startSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	token := randomToken()
	expires := time.Now().Add(sessionTTL)
	if err := h.store.CreateSession(hashToken(token), u.ID, expires); err != nil {
		h.internalError(w, err)
		return
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
	writeJSON(w, http.StatusOK, userDTO{Username: u.Username})
}

func (h *Hub) postLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := h.store.DeleteSession(hashToken(c.Value)); err != nil {
			h.log.Error("deleting session failed", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) getMe(w http.ResponseWriter, _ *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, userDTO{Username: u.Username})
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
