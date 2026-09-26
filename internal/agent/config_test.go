package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Jackolix/Lotse/internal/sshkey"
)

func testHubKey(t *testing.T) string {
	t.Helper()
	signer, err := sshkey.LoadOrCreate(filepath.Join(t.TempDir(), "hub_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return sshkey.AuthorizedKey(signer.PublicKey())
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A config placed in a shared folder (--config /etc/lotse.json) must neither lock
// others out of the folder nor get it deleted by uninstall --purge.
func TestConfigInSharedFolder(t *testing.T) {
	shared := t.TempDir()
	os.Chmod(shared, 0o755)
	other := filepath.Join(shared, "passwd")
	if err := os.WriteFile(other, []byte("root:x:0:0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfig(filepath.Join(shared, "lotse.json"), "http://hub:8090", testHubKey(t), "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := cfg.DedicatedDir(); ok || err != nil {
		t.Fatalf("DedicatedDir = %v, %v for a folder with other files", ok, err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && mode(t, shared) != 0o755 {
		t.Errorf("saving the config changed the shared folder to %v", mode(t, shared))
	}
	if err := PurgeConfig(cfg.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("purge removed another file: %v", err)
	}
	if _, err := os.Stat(cfg.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("purge left the config: %v", err)
	}
}

func TestConfigInOwnFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lotse-agent")
	cfg, err := NewConfig(filepath.Join(dir, "agent.json"), "http://hub:8090", testHubKey(t), "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := cfg.DedicatedDir(); !ok || err != nil {
		t.Fatalf("DedicatedDir = %v, %v for a new folder", ok, err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := sshkey.LoadOrCreate(cfg.KeyPath()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := cfg.DedicatedDir(); !ok {
		t.Error("the agent's own files made its folder look shared")
	}
	if runtime.GOOS != "windows" && mode(t, dir) != 0o700 {
		t.Errorf("config folder mode = %v, want 0700", mode(t, dir))
	}
	if err := PurgeConfig(cfg.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("purge left the folder: %v", err)
	}
}
