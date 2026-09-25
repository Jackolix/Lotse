package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// serviceTimeout bounds a service manager command. Actions return once the service
// manager has accepted them; the state shows up in the next list.
const serviceTimeout = 15 * time.Second

// checkServiceAction validates a request before it reaches the service manager.
func checkServiceAction(name, action string, own ...string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	if name == "" || len(name) > 256 || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n/\\") {
		return errors.New("invalid service name")
	}
	for _, o := range own {
		if strings.EqualFold(name, o) {
			return errors.New("the agent cannot control its own service; reinstall or update it instead")
		}
	}
	return nil
}

// runServiceCommand runs a service manager command and turns its error output into
// the error message.
func runServiceCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, errors.New(msg)
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}
