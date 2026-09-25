package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/version"
)

const (
	defaultScriptTimeout = 5 * time.Minute
	maxScriptTimeout     = 24 * time.Hour
	// exitTimeout is the exit status of a script stopped at its time limit, as with
	// GNU timeout.
	exitTimeout = 124
)

// runScript runs a script with the requested interpreter and writes its output
// (stdout and stderr merged, in order) to out. It returns the exit status. The
// script and everything it started is killed when ctx ends or the time limit passes.
func (a *Agent) runScript(ctx context.Context, out io.Writer, msg protocol.ScriptMsg) uint32 {
	timeout := time.Duration(msg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultScriptTimeout
	}
	timeout = min(timeout, maxScriptTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", version.AgentName+"-script-")
	if err == nil {
		defer os.RemoveAll(dir)
		err = restrictDir(dir) // the script may contain secrets
	}
	if err != nil {
		fmt.Fprintf(out, "cannot create a temporary directory: %v\n", err)
		return 1
	}
	path, args, err := scriptCommand(msg.Shell, dir, msg.Script)
	if err != nil {
		fmt.Fprintf(out, "%v\n", err)
		return 127
	}

	cmd := exec.CommandContext(ctx, path, args...)
	home := homeDir()
	cmd.Dir = home
	cmd.Env = shellEnv("dumb", path, home)
	cmd.Stdout, cmd.Stderr = out, out
	// Background processes that keep the output open do not keep the script running.
	cmd.WaitDelay = 5 * time.Second
	killTreeOnCancel(cmd)

	start := time.Now()
	a.log.Info("script started", "shell", msg.Shell, "timeout", timeout)
	err = cmd.Run()
	var status uint32
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(out, "\n[stopped: the time limit of %s was reached]\n", timeout)
		status = exitTimeout
	case cmd.ProcessState == nil:
		fmt.Fprintf(out, "cannot run the script: %v\n", err)
		status = 127
	case cmd.ProcessState.ExitCode() < 0: // killed by a signal
		status = 255
	default:
		status = uint32(cmd.ProcessState.ExitCode())
	}
	a.log.Info("script finished", "exit_status", status, "duration", time.Since(start).Round(time.Millisecond))
	return status
}

// scriptCommand writes the script into dir and returns how to run it.
func scriptCommand(shell, dir, script string) (string, []string, error) {
	windows := runtime.GOOS == "windows"
	write := func(name, content string) (string, error) {
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, []byte(content), 0o700)
	}
	switch shell {
	case "sh", "bash":
		if windows {
			return "", nil, fmt.Errorf("%s scripts need Linux or macOS", shell)
		}
		interp := "/bin/sh"
		if shell == "bash" {
			var err error
			if interp, err = exec.LookPath("bash"); err != nil {
				return "", nil, errors.New("bash is not installed on this machine")
			}
		}
		file, err := write("script.sh", script)
		return interp, []string{file}, err
	case "powershell":
		names := []string{"pwsh"}
		if windows {
			names = []string{"pwsh.exe", "powershell.exe"}
			// UTF-8 output instead of the console code page; a BOM makes Windows
			// PowerShell 5 read the file as UTF-8 too.
			script = "\ufeff[Console]::OutputEncoding = [Text.Encoding]::UTF8\r\n" + script
		}
		var interp string
		for _, n := range names {
			if p, err := exec.LookPath(n); err == nil {
				interp = p
				break
			}
		}
		if interp == "" {
			return "", nil, errors.New("PowerShell is not installed on this machine")
		}
		file, err := write("script.ps1", script)
		return interp, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file}, err
	case "cmd":
		if !windows {
			return "", nil, errors.New("cmd scripts need Windows")
		}
		interp := os.Getenv("ComSpec")
		if interp == "" {
			interp = "cmd.exe"
		}
		// Batch files need CRLF line endings for labels and goto to work reliably.
		script = strings.ReplaceAll(strings.ReplaceAll(script, "\r\n", "\n"), "\n", "\r\n")
		file, err := write("script.cmd", script)
		return interp, []string{"/d", "/c", file}, err
	}
	return "", nil, fmt.Errorf("unknown shell %q", shell)
}
