package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// UseTestKey makes Verify trust a fresh key for the rest of the test.
func useTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := TrustedKeys
	TrustedKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	t.Cleanup(func() { TrustedKeys = old })
	return priv
}

func TestSignAndVerify(t *testing.T) {
	key := useTestKey(t)
	path := filepath.Join(t.TempDir(), AgentFile("linux", "amd64"))
	os.WriteFile(path, []byte("binary"), 0o755)

	m, err := Sign(path, "1.2.3", key)
	if err != nil {
		t.Fatal(err)
	}
	if m.File != "lotse-agent-linux-amd64" || m.Size != 6 {
		t.Fatalf("manifest = %+v", m)
	}
	if err := m.Verify(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	for name, tamper := range map[string]func(*Manifest){
		"version": func(m *Manifest) { m.Version = "9.9.9" },
		"file":    func(m *Manifest) { m.File = "lotse-agent-windows-amd64.exe" },
		"hash":    func(m *Manifest) { m.SHA256 = "00" + m.SHA256[2:] },
		"size":    func(m *Manifest) { m.Size++ },
	} {
		bad := *m
		tamper(&bad)
		if bad.Verify() == nil {
			t.Errorf("manifest with changed %s still verifies", name)
		}
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	foreign, _ := Sign(path, "1.2.3", other)
	if foreign.Verify() == nil {
		t.Error("manifest signed with an unknown key verifies")
	}
	if _, err := Sign(path, "dev", key); err == nil {
		t.Error("a development version was signed")
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		want               bool
	}{
		{"0.4.0", "0.3.9", true},
		{"0.4.0", "0.4.0", false},
		{"0.3.10", "0.3.9", true},
		{"0.3.9", "0.4.0", false},
		{"1.0.0", "1.0.0-rc.1", true},
		{"1.0.0-rc.2", "1.0.0-rc.1", true},
		{"1.0.0-rc.10", "1.0.0-rc.9", true},
		{"1.0.0-beta", "1.0.0-alpha", true},
		{"1.0.0-rc.1", "1.0.0", false},
		{"0.4.0", "dev", true},
		{"0.4.0", "main-abc1234", true},
		{"dev", "0.1.0", false},
		{"v1.2.3", "1.2.2", true},
		{"01.2.3", "1.2.2", false},
		{"1.2", "1.1.0", false},
	} {
		if got := Newer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}
