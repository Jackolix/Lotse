//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

func requireAdmin() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("this command must run in an elevated (Administrator) PowerShell")
	}
	return nil
}
