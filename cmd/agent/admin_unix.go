//go:build !windows

package main

import (
	"errors"
	"os"
)

func requireAdmin() error {
	if os.Geteuid() != 0 {
		return errors.New("this command must run as root (use sudo)")
	}
	return nil
}
