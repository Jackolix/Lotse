package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// powerCommand returns the command that reboots or shuts down this machine. It is
// looked up before answering the hub, and run after.
func powerCommand(action string) (*exec.Cmd, error) {
	if action != "reboot" && action != "shutdown" {
		return nil, fmt.Errorf("unknown power action %q", action)
	}
	reboot := action == "reboot"
	var candidates [][]string
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			if reboot {
				candidates = append(candidates, []string{"systemctl", "reboot"})
			} else {
				candidates = append(candidates, []string{"systemctl", "poweroff"})
			}
		}
		if reboot {
			candidates = append(candidates, []string{"shutdown", "-r", "now"}, []string{"reboot"})
		} else {
			candidates = append(candidates, []string{"shutdown", "-h", "now"}, []string{"poweroff"})
		}
	case "darwin":
		flag := "-h"
		if reboot {
			flag = "-r"
		}
		candidates = [][]string{{"/sbin/shutdown", flag, "now"}}
	case "windows":
		flag := "/s"
		if reboot {
			flag = "/r"
		}
		// Planned shutdown, 5 seconds' notice, apps are closed without asking.
		candidates = [][]string{{"shutdown.exe", flag, "/f", "/t", "5", "/d", "p:0:0", "/c", "Requested from Lotse"}}
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c[0]); err == nil {
			return exec.Command(p, c[1:]...), nil
		}
	}
	return nil, errors.New("found no command to " + action + " this machine")
}
