package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/update"
	"github.com/Jackolix/Lotse/internal/version"
)

// maxUpdateSize bounds a new binary; agents are about 10 MB.
const maxUpdateSize = 200 << 20

// handleUpdate installs a new agent binary sent by the hub. The manifest must carry
// a valid release signature, match this platform and be newer than the running
// version; the hub cannot push anything else.
func (a *Agent) handleUpdate(nc ssh.NewChannel) {
	reject := func(msg string) {
		a.log.Warn("refused update", "reason", msg)
		nc.Reject(ssh.Prohibited, msg)
	}
	if a.cfg.DisableUpdates {
		reject("updates are disabled on this machine (installed with --no-updates)")
		return
	}
	var m update.Manifest
	if err := json.Unmarshal(nc.ExtraData(), &m); err != nil {
		reject("invalid update manifest")
		return
	}
	if err := m.Verify(); err != nil {
		reject(err.Error())
		return
	}
	if want := update.AgentFile(runtime.GOOS, runtime.GOARCH); m.File != want {
		reject(fmt.Sprintf("the update is %s, this machine needs %s", m.File, want))
		return
	}
	if !update.Newer(m.Version, version.Version) {
		reject(fmt.Sprintf("version %s is not newer than the installed %s", m.Version, version.Version))
		return
	}
	if m.Size > maxUpdateSize {
		reject("the update is too large")
		return
	}
	if !a.updating.CompareAndSwap(false, true) {
		reject("an update is already in progress")
		return
	}
	defer a.updating.Store(false)
	exe, err := a.executable()
	if err != nil {
		reject("cannot find the agent binary: " + err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	defer ch.Close()

	a.log.Info("installing update", "version", m.Version, "binary", exe)
	err = install(exe, ch, &m)
	var res protocol.UpdateResult
	if err != nil {
		res.Error = err.Error()
		a.log.Warn("update failed", "err", err)
	}
	json.NewEncoder(ch).Encode(res)
	ch.CloseWrite()
	if err != nil {
		return
	}
	a.log.Info("update installed, restarting", "version", m.Version)
	if a.Restart != nil {
		time.AfterFunc(500*time.Millisecond, func() { a.Restart(exe) })
	}
}

func (a *Agent) executable() (string, error) {
	if a.Executable != "" {
		return a.Executable, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// install writes the binary next to exe, checks its hash and that it runs, and
// then swaps it in. Running processes keep the old file until they restart.
func install(exe string, r io.Reader, m *update.Manifest) error {
	pattern := "." + filepath.Base(exe) + "-update-*"
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), pattern)
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, m.Size))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("receiving the update failed: %w", err)
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return errors.New("the received binary does not match the signed checksum")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	// The new binary must start on this machine and report the signed version.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmp.Name(), "version").Output()
	if err != nil || !strings.Contains(string(out), m.Version) {
		return fmt.Errorf("the new binary does not run on this machine (%v)", err)
	}

	if runtime.GOOS != "windows" {
		return os.Rename(tmp.Name(), exe)
	}
	// Windows cannot replace a running executable, but it can rename it.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

// cleanupUpdate removes what a previous update on Windows left behind.
func (a *Agent) cleanupUpdate() {
	if exe, err := a.executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
