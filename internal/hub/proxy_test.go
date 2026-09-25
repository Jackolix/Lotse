package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProxies(t *testing.T) {
	p, err := parseProxies(" 192.168.1.20, 172.16.0.0/12 ,fd00::/8,")
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 3 || p[0].String() != "192.168.1.20/32" || p[1].String() != "172.16.0.0/12" {
		t.Fatalf("parsed %v", p)
	}
	if p, err := parseProxies(""); err != nil || p != nil {
		t.Fatalf("empty list: %v, %v", p, err)
	}
	if _, err := parseProxies("192.168.1.20, cloudflared"); err == nil {
		t.Fatal("accepted a hostname")
	}
}

func TestRealIP(t *testing.T) {
	proxies, _ := parseProxies("10.0.0.2, 172.18.0.0/16")
	h := &Hub{proxies: proxies}
	var got string
	handler := h.realIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clientIP(r) }))

	for _, tc := range []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct client", "192.168.1.50:5000", nil, "192.168.1.50"},
		{"untrusted peer cannot claim an address", "192.168.1.50:5000", []string{"1.2.3.4"}, "192.168.1.50"},
		{"trusted proxy", "10.0.0.2:5000", []string{"203.0.113.9"}, "203.0.113.9"},
		{"forged entries left of the client are ignored", "10.0.0.2:5000", []string{"1.2.3.4, 203.0.113.9"}, "203.0.113.9"},
		{"chained trusted proxies", "10.0.0.2:5000", []string{"203.0.113.9, 172.18.0.7"}, "203.0.113.9"},
		{"repeated headers", "10.0.0.2:5000", []string{"1.2.3.4", "2001:db8::1"}, "2001:db8::1"},
		{"no header keeps the proxy", "10.0.0.2:5000", nil, "10.0.0.2"},
		{"garbage keeps the proxy", "10.0.0.2:5000", []string{"203.0.113.9, unknown"}, "10.0.0.2"},
		{"only proxies keeps the peer", "10.0.0.2:5000", []string{"172.18.0.7"}, "10.0.0.2"},
		{"IPv4-mapped peer", "[::ffff:10.0.0.2]:5000", []string{"203.0.113.9"}, "203.0.113.9"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		for _, v := range tc.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if got != tc.want {
			t.Errorf("%s: client %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Failed logins through a proxy must lock out the client, not the proxy.
func TestLockoutBehindProxy(t *testing.T) {
	h, err := New(Config{DataDir: t.TempDir(), AgentDir: t.TempDir(), TrustedProxies: "10.0.0.2"}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.store.Close() })
	handler := h.Handler()
	login := func(client string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"x","password":"wrong-password"}`))
		r.RemoteAddr = "10.0.0.2:5000"
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	for range loginFailures {
		login("203.0.113.9")
	}
	if code := login("203.0.113.9"); code != http.StatusTooManyRequests {
		t.Fatalf("attacker not locked out: %d", code)
	}
	if code := login("198.51.100.4"); code != http.StatusUnauthorized {
		t.Fatalf("another client behind the same proxy got %d", code)
	}
}
