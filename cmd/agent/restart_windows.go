//go:build windows

package main

import (
	"log/slog"
	"os"

	"github.com/kardianos/service"
)

// restart exits so the service manager starts the updated binary: the service is
// set up to restart after a failure.
func restart(_ string, log *slog.Logger) {
	if service.Interactive() {
		log.Info("update installed; start the agent again to run the new version")
	}
	os.Exit(1)
}
