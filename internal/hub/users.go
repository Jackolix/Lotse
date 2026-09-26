package hub

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// Administrators manage the other accounts. Every change needs a recent
// re-authentication and is audited; the last administrator cannot be removed or
// demoted.

type userDTO struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	TOTP      bool   `json:"totp"`
	Passkeys  int    `json:"passkeys"`
	CreatedAt int64  `json:"created_at"`
	LastLogin int64  `json:"last_login"`
}

func (h *Hub) getUsers(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	users, err := h.store.Users()
	if err != nil {
		h.internalError(w, err)
		return
	}
	counts, err := h.store.PasskeyCounts()
	if err != nil {
		h.internalError(w, err)
		return
	}
	out := make([]userDTO, len(users))
	for i, u := range users {
		out[i] = userDTO{ID: u.ID, Username: u.Username, Role: u.Role, TOTP: u.TOTPSecret != "", Passkeys: counts[u.ID],
			CreatedAt: u.CreatedAt, LastLogin: u.LastLogin}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) postUser(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		credentials
		Role string `json:"role"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !requireElevated(w, s) {
		return
	}
	if msg := body.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !store.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "role must be viewer, operator or admin")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcryptCost)
	if err != nil {
		h.internalError(w, err)
		return
	}
	u, err := h.store.CreateUser(body.Username, string(hash), body.Role)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "user_created", nil, fmt.Sprintf("%s (%s)", u.Username, u.Role))
	writeJSON(w, http.StatusOK, userDTO{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt})
}

// patchUser changes a role, sets a new password, turns off two-factor login for
// someone who lost their authenticator, or removes passkeys, e.g. one an intruder
// added to the account.
func (h *Hub) patchUser(w http.ResponseWriter, r *http.Request, s *store.Session) {
	u, ok := h.lookupUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Role          string `json:"role"`
		Password      string `json:"password"`
		ResetTOTP     bool   `json:"reset_totp"`
		ResetPasskeys bool   `json:"reset_passkeys"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !requireElevated(w, s) {
		return
	}
	if body.Role != "" && !store.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "role must be viewer, operator or admin")
		return
	}
	if body.Password != "" {
		if msg := validatePassword(body.Password); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}

	// Whoever signed in with an old password or a removed passkey is signed out,
	// except the admin making the change.
	keep := []byte(nil)
	if u.ID == s.ID {
		keep = s.TokenHash
	}
	signOut := false

	var changes []string
	if body.Role != "" && body.Role != u.Role {
		if err := h.store.SetRole(u.ID, body.Role); err != nil {
			h.userError(w, err)
			return
		}
		changes = append(changes, fmt.Sprintf("role %s → %s", u.Role, body.Role))
		h.closeConnsBeyondRole(u.ID, body.Role)
	}
	if body.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcryptCost)
		if err != nil {
			h.internalError(w, err)
			return
		}
		if err := h.store.SetPassword(u.ID, string(hash)); err != nil {
			h.internalError(w, err)
			return
		}
		signOut = true
		changes = append(changes, "new password")
	}
	if body.ResetTOTP && u.TOTPSecret != "" {
		if err := h.store.SetTOTPSecret(u.ID, ""); err != nil {
			h.internalError(w, err)
			return
		}
		changes = append(changes, "two-factor login turned off")
	}
	if body.ResetPasskeys {
		n, err := h.store.DeletePasskeysOf(u.ID)
		if err != nil {
			h.internalError(w, err)
			return
		}
		if n > 0 {
			signOut = true
			changes = append(changes, fmt.Sprintf("%d passkey(s) removed", n))
		}
	}
	if signOut {
		if err := h.signOutUser(u.ID, keep); err != nil {
			h.log.Error("ending sessions failed", "err", err)
		}
	}
	if len(changes) > 0 {
		h.audit(r, s.Username, "user_changed", nil, u.Username+": "+strings.Join(changes, ", "))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) deleteUser(w http.ResponseWriter, r *http.Request, s *store.Session) {
	u, ok := h.lookupUser(w, r)
	if !ok {
		return
	}
	if !requireElevated(w, s) {
		return
	}
	if u.ID == s.ID {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	if err := h.store.DeleteUser(u.ID); err != nil {
		h.userError(w, err)
		return
	}
	h.closeConns(func(c *sessionConn) bool { return c.userID == u.ID })
	h.audit(r, s.Username, "user_deleted", nil, u.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) lookupUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u, err := h.store.User(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return nil, false
	}
	if err != nil {
		h.internalError(w, err)
		return nil, false
	}
	return u, true
}

func (h *Hub) userError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, err.Error()+"; make someone else an administrator first")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
	default:
		h.internalError(w, err)
	}
}
