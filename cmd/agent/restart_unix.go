//go:build !windows

package main

import (
	"log/slog"
	"os"
	"syscall"
)

// restart replaces the process with the updated binary. The PID stays the same, so
// systemd and launchd keep tracking it. If that fails, exiting lets the service
// manager start the new binary.
func restart(exe string, log *slog.Logger) {
	err := syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ())
	log.Error("restarting after the update failed, exiting so the service manager restarts the agent", "err", err)
	os.Exit(1)
}
