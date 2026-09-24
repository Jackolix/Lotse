package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"time"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/protocol"
)

// maxShells bounds concurrent remote shells on one machine.
const maxShells = 8

// handleChannels serves SSH channels opened by the hub. The only kind is "session",
// accepted only if the machine's owner allowed remote shells when installing.
func (a *Agent) handleChannels(chans <-chan ssh.NewChannel) {
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		if !a.cfg.AllowShell {
			nc.Reject(ssh.Prohibited, "remote shell is disabled on this machine; reinstall the agent with --allow-shell to enable it")
			continue
		}
		if a.shells.Add(1) > maxShells {
			a.shells.Add(-1)
			nc.Reject(ssh.ResourceShortage, "too many open shells")
			continue
		}
		ch, reqs, err := nc.Accept()
		if err != nil {
			a.shells.Add(-1)
			continue
		}
		go func() {
			defer a.shells.Add(-1)
			a.serveSession(ch, reqs)
		}()
	}
}

// serveSession implements the subset of RFC 4254 a terminal needs:
// pty-req, shell and window-change.
func (a *Agent) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var ptyReq *protocol.PtyRequest
	var sh *shell
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p protocol.PtyRequest
			if sh != nil || ssh.Unmarshal(req.Payload, &p) != nil {
				req.Reply(false, nil)
				continue
			}
			ptyReq = &p
			req.Reply(true, nil)
		case "window-change":
			var w protocol.WindowChange
			if sh != nil && ssh.Unmarshal(req.Payload, &w) == nil {
				sh.resize(w.Columns, w.Rows)
			}
			req.Reply(true, nil)
		case "shell":
			if sh != nil || ptyReq == nil {
				req.Reply(false, nil)
				continue
			}
			var err error
			if sh, err = startShell(ptyReq); err != nil {
				a.log.Warn("starting shell failed", "err", err)
				fmt.Fprintf(ch.Stderr(), "cannot start a shell: %v\r\n", err)
				req.Reply(false, nil)
				return
			}
			req.Reply(true, nil)
			a.log.Info("remote shell opened", "shell", sh.path, "pid", sh.cmd.Process.Pid)
			go func() {
				status := sh.run(ch)
				a.log.Info("remote shell closed", "pid", sh.cmd.Process.Pid, "exit_status", status)
				ch.SendRequest("exit-status", false, ssh.Marshal(protocol.ExitStatus{Status: status}))
				ch.Close()
			}()
		default: // env, exec, subsystem, x11-req, ...
			req.Reply(false, nil)
		}
	}
	// The channel is gone (closed by either side or the link dropped).
	if sh != nil {
		sh.kill()
	}
}

type shell struct {
	pty  pty.Pty
	cmd  *pty.Cmd
	path string
}

func startShell(req *protocol.PtyRequest) (*shell, error) {
	path, args := shellCommand()
	p, err := pty.New()
	if err != nil {
		return nil, err
	}
	_ = p.Resize(dim(req.Columns, 80), dim(req.Rows, 24))

	home := homeDir()
	cmd := p.Command(path, args...)
	cmd.Env = shellEnv(req.Term, path, home)
	cmd.Dir = home
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, err
	}
	// Drop our copy of the terminal's slave end, so reads fail once the shell and
	// everything it started have exited.
	if u, ok := p.(pty.UnixPty); ok {
		u.Slave().Close()
	}
	return &shell{pty: p, cmd: cmd, path: path}, nil
}

// run connects the shell to the channel until the shell exits and returns its exit status.
func (s *shell) run(ch ssh.Channel) uint32 {
	go io.Copy(s.pty, ch) // keystrokes
	output := make(chan struct{})
	go func() {
		io.Copy(ch, s.pty)
		close(output)
	}()

	err := s.cmd.Wait()
	// Let the last output drain, then close the terminal. Background jobs that still
	// hold it open do not keep the session alive.
	select {
	case <-output:
	case <-time.After(300 * time.Millisecond):
	}
	s.pty.Close()
	<-output

	if s.cmd.ProcessState != nil {
		return uint32(max(s.cmd.ProcessState.ExitCode(), 0))
	}
	if err != nil {
		return 1
	}
	return 0
}

func (s *shell) resize(cols, rows uint32) {
	_ = s.pty.Resize(dim(cols, 80), dim(rows, 24))
}

// kill ends the shell when the hub side went away. Closing the terminal hangs up
// the shell's process group; the explicit kill covers shells that ignore SIGHUP.
func (s *shell) kill() {
	if s.cmd.ProcessState == nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pty.Close()
}

func dim(v uint32, def int) int {
	if v == 0 || v > 1000 {
		return def
	}
	return int(v)
}

// shellCommand picks the interactive shell: PowerShell on Windows, otherwise the
// user's login shell.
func shellCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		for _, name := range []string{"pwsh.exe", "powershell.exe"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, []string{"-NoLogo"}
			}
		}
		if c := os.Getenv("ComSpec"); c != "" {
			return c, nil
		}
		return "cmd.exe", nil
	}
	candidates := []string{os.Getenv("SHELL"), "/bin/bash", "/bin/zsh", "/bin/sh"}
	if runtime.GOOS == "darwin" {
		candidates = []string{os.Getenv("SHELL"), "/bin/zsh", "/bin/bash", "/bin/sh"}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, []string{"-l"} // login shell: loads the profile, as over SSH
		}
	}
	return "/bin/sh", []string{"-l"}
}

func homeDir() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if fi, err := os.Stat(u.HomeDir); err == nil && fi.IsDir() {
			return u.HomeDir
		}
	}
	if runtime.GOOS == "windows" {
		return os.Getenv("SystemDrive") + `\`
	}
	return "/"
}

func shellEnv(term, shellPath, home string) []string {
	if term == "" {
		term = "xterm-256color"
	}
	env := os.Environ()
	if runtime.GOOS == "windows" {
		return env
	}
	set := map[string]string{"TERM": term, "SHELL": shellPath, "HOME": home}
	if u, err := user.Current(); err == nil {
		set["USER"], set["LOGNAME"] = u.Username, u.Username
	}
	if os.Getenv("PATH") == "" {
		set["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	if os.Getenv("LANG") == "" {
		set["LANG"] = "C.UTF-8"
	}
	out := env[:0:0]
	for _, kv := range env {
		keep := true
		for k := range set {
			if len(kv) > len(k) && kv[:len(k)+1] == k+"=" {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}
