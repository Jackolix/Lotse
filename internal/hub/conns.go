package hub

import (
	"bytes"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// sessionConn is a long-lived connection that acts with a browser session's rights:
// a terminal, a file transfer or an event stream. They are tracked so that signing
// out, a password change, a deleted account or a lost role ends them right away,
// not only the next request.
type sessionConn struct {
	session []byte // token hash of the browser session
	userID  int64
	role    string // the role the connection needs
	stop    func()
}

// track registers a connection of session s that needs role, until the returned
// function is called. stop must end the connection.
func (h *Hub) track(s *store.Session, role string, stop func()) (untrack func()) {
	c := &sessionConn{session: s.TokenHash, userID: s.ID, role: role, stop: stop}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.conns, c)
		h.mu.Unlock()
	}
}

// closeConns ends the tracked connections that match. They are stopped outside the
// lock, since closing an SSH channel can wait for the network.
func (h *Hub) closeConns(match func(c *sessionConn) bool) {
	var stop []func()
	h.mu.Lock()
	for c := range h.conns {
		if match(c) {
			stop = append(stop, c.stop)
		}
	}
	h.mu.Unlock()
	for _, fn := range stop {
		fn()
	}
}

// closeSessionConns ends the connections of one browser session, on sign-out.
func (h *Hub) closeSessionConns(tokenHash []byte) {
	h.closeConns(func(c *sessionConn) bool { return bytes.Equal(c.session, tokenHash) })
}

// closeConnsBeyondRole ends a user's connections that their new role no longer allows.
func (h *Hub) closeConnsBeyondRole(userID int64, role string) {
	h.closeConns(func(c *sessionConn) bool { return c.userID == userID && !store.RoleIncludes(role, c.role) })
}

// signOutUser ends every session of a user and their connections, except the
// session keep (the one making the change; nil for none).
func (h *Hub) signOutUser(userID int64, keep []byte) error {
	var err error
	if keep != nil {
		err = h.store.DeleteOtherSessions(userID, keep)
	} else {
		err = h.store.DeleteSessions(userID)
	}
	h.closeConns(func(c *sessionConn) bool { return c.userID == userID && !bytes.Equal(c.session, keep) })
	return err
}
