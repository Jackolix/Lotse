package hub

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

type testClient struct {
	t   *testing.T
	c   *http.Client
	url string
}

func newClient(t *testing.T, url string) *testClient {
	jar, _ := cookiejar.New(nil)
	return &testClient{t: t, c: &http.Client{Jar: jar}, url: url}
}

func (tc *testClient) post(path string, body any) (int, map[string]any) {
	tc.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := tc.c.Post(tc.url+path, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		tc.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTOTPLogin(t *testing.T) {
	h, srv := newTestHub(t)
	_, u := newSession(t, h, false)
	key, _ := totp.Generate(totp.GenerateOpts{Issuer: "Lotse", AccountName: "admin"})
	if err := h.store.SetTOTPSecret(u.ID, key.Secret()); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, srv.URL)

	status, body := c.post("/api/login", map[string]string{"username": "admin", "password": testPassword})
	if status != http.StatusUnauthorized || body["totp_required"] != true {
		t.Fatalf("login without code = %d %v, want 401 totp_required", status, body)
	}
	status, _ = c.post("/api/login", map[string]string{"username": "admin", "password": testPassword, "code": "000000"})
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong code accepted: %d", status)
	}
	code, _ := totp.GenerateCode(key.Secret(), time.Now())
	status, _ = c.post("/api/login", map[string]string{"username": "admin", "password": testPassword, "code": code})
	if status != http.StatusOK {
		t.Fatalf("login with code = %d", status)
	}
	// The same code cannot be used twice.
	status, _ = newClient(t, srv.URL).post("/api/login", map[string]string{"username": "admin", "password": testPassword, "code": code})
	if status != http.StatusUnauthorized {
		t.Fatalf("replayed code accepted: %d", status)
	}
}

func TestElevateAndEnableTOTP(t *testing.T) {
	h, srv := newTestHub(t)
	newSession(t, h, false)
	c := newClient(t, srv.URL)
	if status, _ := c.post("/api/login", map[string]string{"username": "admin", "password": testPassword}); status != http.StatusOK {
		t.Fatalf("login = %d", status)
	}

	if status, _ := c.post("/api/elevate", map[string]string{"password": "nope-nope"}); status != http.StatusForbidden {
		t.Fatalf("elevate with wrong password = %d", status)
	}
	status, body := c.post("/api/elevate", map[string]string{"password": testPassword})
	if status != http.StatusOK || body["elevated_until"].(float64) < float64(time.Now().Unix()) {
		t.Fatalf("elevate = %d %v", status, body)
	}

	_, setup := c.post("/api/me/totp/setup", nil)
	secret, _ := setup["secret"].(string)
	if secret == "" || !strings.HasPrefix(setup["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("setup response: %v", setup)
	}
	if status, _ := c.post("/api/me/totp/enable", map[string]string{"secret": secret, "code": "123456", "password": testPassword}); status != http.StatusBadRequest {
		t.Fatalf("enable with wrong code = %d", status)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if status, _ := c.post("/api/me/totp/enable", map[string]string{"secret": secret, "code": code, "password": testPassword}); status != http.StatusNoContent {
		t.Fatalf("enable = %d", status)
	}
	u, _ := h.store.UserByName("admin")
	if u.TOTPSecret != secret {
		t.Fatal("secret not stored")
	}

	entries, _ := h.store.Audit(0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"login", "reauth_failed", "reauth", "totp_enabled"} {
		if !strings.Contains(strings.Join(actions, " "), want) {
			t.Errorf("audit log misses %q: %v", want, actions)
		}
	}
}
