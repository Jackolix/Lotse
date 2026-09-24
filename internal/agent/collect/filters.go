package collect

import (
	"os"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// Filesystem types that never represent user-visible storage.
var skipFSTypes = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "iso9660": true,
	"ramfs": true, "nsfs": true, "fuse.snapfuse": true, "devfs": true, "autofs": true, "nullfs": true,
}

func keepFilesystem(p disk.PartitionStat) bool {
	if skipFSTypes[p.Fstype] {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		// "/" plus external volumes. The /System/Volumes/* APFS volumes share the
		// root container and would be counted several times.
		return p.Mountpoint == "/" || strings.HasPrefix(p.Mountpoint, "/Volumes/")
	case "linux":
		return !strings.HasPrefix(p.Mountpoint, "/snap/") && !strings.HasPrefix(p.Mountpoint, "/boot")
	}
	return true
}

// Interfaces whose traffic is either local or also counted on a physical NIC.
var virtualIfPrefixes = []string{
	"lo", "docker", "veth", "br-", "virbr", "vmnet", "vboxnet", "cni", "flannel", "cali", "kube",
	"tun", "tap", "wg", "tailscale", "zt", "utun", "awdl", "llw", "bridge", "anpi", "ap1", "gif", "stf",
	"Loopback", "vEthernet", "isatap", "Teredo", "Local Area Connection*",
}

func virtualInterface(name string) bool {
	for _, p := range virtualIfPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Linux block devices that sit on top of other disks and would double count I/O.
var stackedDiskPrefixes = []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd", "nbd"}

func physicalDisk(name string) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	for _, p := range stackedDiskPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	// Whole disks are listed in /sys/block, partitions (sda1, nvme0n1p1) are not.
	_, err := os.Stat("/sys/block/" + name)
	return err == nil
}
