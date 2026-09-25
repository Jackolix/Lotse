//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// killTreeOnCancel runs the command in its own process group, so cancelling kills
// the script and everything it started.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
