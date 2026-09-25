//go:build !linux

package main

// dropPrivileges only matters in the Linux container image.
func dropPrivileges(string) error { return nil }
