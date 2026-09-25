package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// dropPrivileges runs first when the hub starts as root, which the Docker image
// does by default. It hands the data directory to the service user and switches
// to that user, so bind mounts that NAS app managers create as root just work.
// PUID/PGID choose the user (default 65532, "nonroot"); PUID=0 keeps root.
func dropPrivileges(dataDir string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	uid, err := envID("PUID")
	if err != nil {
		return err
	}
	gid, err := envID("PGID")
	if err != nil {
		return err
	}
	if uid == 0 {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	err = filepath.WalkDir(dataDir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
	if err != nil {
		return fmt.Errorf("handing %s to %d:%d: %w", dataDir, uid, gid, err)
	}
	// Order matters: supplementary groups and group first, the user last.
	if err := syscall.Setgroups(nil); err != nil {
		return err
	}
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	return syscall.Setuid(uid)
}

func envID(name string) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return 65532, nil
	}
	id, err := strconv.Atoi(v)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%s must be a numeric ID, got %q", name, v)
	}
	return id, nil
}
