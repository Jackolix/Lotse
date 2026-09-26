package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// sessionFor creates a user with a role (once) and a session, returning its cookie.
func sessionFor(t *testing.T, h *Hub, username, role string, elevated bool) (string, *store.User) {
	t.Helper()
	u, err := h.store.UserByName(username)
	if err != nil {
		hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
		if u, err = h.store.CreateUser(username, string(hash), role); err != nil {
			t.Fatal(err)
		}
	}
	token := randomToken()
	if err := h.store.CreateSession(hashToken(token), u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if elevated {
		h.store.ElevateSession(hashToken(token), time.Now().Add(time.Minute))
	}
	return token, u
}

// call sends an authenticated JSON request and returns the status and decoded body.
func call(t *testing.T, method, url, cookie string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("Cookie", "session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRolesLimitWhatUsersCanDo(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	viewer, _ := sessionFor(t, h, "vera", store.RoleViewer, true)
	operator, _ := sessionFor(t, h, "otto", store.RoleOperator, true)
	admin, _ := sessionFor(t, h, "ada", store.RoleAdmin, true)
	sysURL := fmt.Sprintf("%s/api/systems/%d", srv.URL, sys.ID)

	for _, tc := range []struct {
		method, url string
		body        any
		allowed     string // least role that may do this
	}{
		{"GET", srv.URL + "/api/systems", nil, store.RoleViewer},
		{"GET", sysURL + "/metrics?range=1h", nil, store.RoleViewer},
		{"GET", srv.URL + "/api/scripts", nil, store.RoleOperator},
		{"GET", sysURL + "/files?path=/", nil, store.RoleOperator},
		{"PATCH", sysURL, map[string]string{"name": "renamed"}, store.RoleOperator},
		{"POST", srv.URL + "/api/enroll", nil, store.RoleAdmin},
		{"GET", srv.URL + "/api/users", nil, store.RoleAdmin},
		{"POST", srv.URL + "/api/alert-rules", map[string]any{"metric": "cpu", "threshold": 80, "duration": 60}, store.RoleAdmin},
	} {
		for _, who := range []struct{ role, cookie string }{{store.RoleViewer, viewer}, {store.RoleOperator, operator}, {store.RoleAdmin, admin}} {
			status, body := call(t, tc.method, tc.url, who.cookie, tc.body)
			want := (&store.User{Role: who.role}).Can(tc.allowed)
			if got := status < 300; got != want {
				t.Errorf("%s %s as %s: status %d %v, allowed = %v", tc.method, tc.url, who.role, status, body, want)
			}
		}
	}

	// Viewers see alert channels without their configuration (webhook URLs are secrets).
	h.store.SaveNotifier(&store.Notifier{Name: "hook", Type: "webhook", Config: `{"url":"https://secret.example/hook"}`, Enabled: true})
	for _, who := range []struct {
		cookie string
		see    bool
	}{{viewer, false}, {admin, true}} {
		_, raw := authedGet(t, srv.URL+"/api/notifiers", who.cookie)
		if strings.Contains(string(raw), "secret.example") != who.see {
			t.Errorf("notifier config visible = %v, want %v: %s", !who.see, who.see, raw)
		}
	}

	// Viewers only see their own activity.
	_, raw := authedGet(t, srv.URL+"/api/audit", viewer)
	var page struct{ Entries []store.AuditEntry }
	json.Unmarshal(raw, &page)
	for _, e := range page.Entries {
		if e.Username != "vera" {
			t.Errorf("viewer sees someone else's activity: %+v", e)
		}
	}
}

func TestUserManagement(t *testing.T) {
	h, srv := newTestHub(t)
	admin, me := sessionFor(t, h, "ada", store.RoleAdmin, true)
	plain, _ := sessionFor(t, h, "ada", store.RoleAdmin, false)

	if status, body := call(t, "POST", srv.URL+"/api/users", plain, map[string]string{"username": "bob", "password": testPassword, "role": "viewer"}); status != http.StatusForbidden || body["reauth_required"] != true {
		t.Fatalf("creating a user without re-authentication = %d %v", status, body)
	}
	status, body := call(t, "POST", srv.URL+"/api/users", admin, map[string]string{"username": "bob", "password": testPassword, "role": "viewer"})
	if status != http.StatusOK {
		t.Fatalf("create = %d %v", status, body)
	}
	bobID := int64(body["id"].(float64))
	if status, _ := call(t, "POST", srv.URL+"/api/users", admin, map[string]string{"username": "BOB", "password": testPassword, "role": "viewer"}); status != http.StatusConflict {
		t.Errorf("duplicate username = %d, want 409", status)
	}
	if status, _ := call(t, "POST", srv.URL+"/api/users", admin, map[string]string{"username": "eve", "password": testPassword, "role": "root"}); status != http.StatusBadRequest {
		t.Errorf("unknown role = %d, want 400", status)
	}

	// Bob signs in, then an admin resets his password: his session ends.
	bob := newClient(t, srv.URL)
	if status, body := bob.post("/api/login", map[string]string{"username": "bob", "password": testPassword}); status != http.StatusOK || body["role"] != "viewer" {
		t.Fatalf("bob's login = %d %v", status, body)
	}
	userURL := fmt.Sprintf("%s/api/users/%d", srv.URL, bobID)
	if status, _ := call(t, "PATCH", userURL, admin, map[string]string{"password": "another password", "role": "operator"}); status != http.StatusNoContent {
		t.Fatalf("patch = %d", status)
	}
	if status, _ := bob.post("/api/elevate", map[string]string{"password": testPassword}); status != http.StatusUnauthorized {
		t.Errorf("bob's session survived a password reset: %d", status)
	}
	u, _ := h.store.User(bobID)
	if u.Role != store.RoleOperator {
		t.Errorf("role = %s", u.Role)
	}

	// The last administrator can be neither demoted nor deleted.
	myURL := fmt.Sprintf("%s/api/users/%d", srv.URL, me.ID)
	if status, _ := call(t, "PATCH", myURL, admin, map[string]string{"role": "viewer"}); status != http.StatusConflict {
		t.Errorf("demoting the last admin = %d, want 409", status)
	}
	if status, _ := call(t, "DELETE", myURL, admin, nil); status != http.StatusBadRequest {
		t.Errorf("deleting yourself = %d, want 400", status)
	}
	if err := h.store.DeleteUser(me.ID); err != store.ErrLastAdmin {
		t.Errorf("store deleted the last admin: %v", err)
	}
	// With a second admin, demoting works.
	call(t, "PATCH", userURL, admin, map[string]string{"role": "admin"})
	if status, _ := call(t, "PATCH", myURL, admin, map[string]string{"role": "operator"}); status != http.StatusNoContent {
		t.Errorf("demoting with another admin present = %d", status)
	}

	if status, _ := call(t, "DELETE", userURL, admin, nil); status != http.StatusForbidden {
		t.Errorf("a demoted admin could still delete users: %d", status)
	}
}

// A passkey an intruder added survives a new password; admins can remove it.
func TestAdminRemovesPasskeys(t *testing.T) {
	h, srv := newTestHub(t)
	admin, _ := sessionFor(t, h, "ada", store.RoleAdmin, true)
	bob, bobUser := sessionFor(t, h, "bob", store.RoleOperator, false)
	key := &store.Passkey{UserID: bobUser.ID, Name: "intruder", RPID: "localhost", CredentialID: []byte("cred"),
		PublicKey: []byte("key"), Algorithm: algES256}
	if err := h.store.CreatePasskey(key); err != nil {
		t.Fatal(err)
	}

	url := fmt.Sprintf("%s/api/users/%d", srv.URL, bobUser.ID)
	if status, body := call(t, "PATCH", url, admin, map[string]bool{"reset_passkeys": true}); status != http.StatusNoContent {
		t.Fatalf("removing passkeys = %d %v", status, body)
	}
	if keys, _ := h.store.Passkeys(bobUser.ID); len(keys) != 0 {
		t.Errorf("%d passkeys left", len(keys))
	}
	if status, _ := call(t, "GET", srv.URL+"/api/me", bob, nil); status != http.StatusUnauthorized {
		t.Errorf("a session survived removing the passkeys: %d", status)
	}
	entries, _ := h.store.Audit(0, 10)
	if len(entries) == 0 || entries[0].Action != "user_changed" || !strings.Contains(entries[0].Detail, "1 passkey(s) removed") {
		t.Errorf("audit log: %+v", entries)
	}
}
