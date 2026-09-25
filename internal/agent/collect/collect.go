// Package collect gathers host metrics with gopsutil. Every call is cheap enough to
// run every couple of seconds; nothing here uses WMI on the hot path.
package collect

import (
	"fmt"
	stdnet "net"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/Jackolix/Lotse/internal/protocol"
)

// maxFilesystems bounds the per-sample filesystem list.
const maxFilesystems = 32

// Collector turns cumulative counters (CPU time, bytes sent, ...) into per-interval
// values, so it keeps the previous reading. It is not safe for concurrent use.
type Collector struct {
	primaryMount string
	docker       docker

	prevTime time.Time
	prevCPU  *cpu.TimesStat
	prevNet  counters
	prevDisk counters
}

type counters struct{ a, b uint64 }

func New() *Collector {
	c := &Collector{primaryMount: primaryMount()}
	c.Sample() // establish baselines so the first real sample has rates
	return c
}

// Info returns static host information.
func Info() protocol.SystemInfo {
	info := protocol.SystemInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if hi, err := host.Info(); err == nil {
		info.Hostname = hi.Hostname
		info.Platform = hi.Platform
		info.PlatformVersion = hi.PlatformVersion
		info.Kernel = hi.KernelVersion
	}
	if info.Hostname == "" {
		info.Hostname, _ = os.Hostname()
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		info.CPUModel = strings.TrimSpace(ci[0].ModelName)
	}
	info.Cores, _ = cpu.Counts(true)
	if vm, err := mem.VirtualMemory(); err == nil {
		info.MemTotal = vm.Total
	}
	info.Interfaces = interfaces()
	return info
}

// interfaces lists physical interfaces that have an address. The hub uses their
// MACs and subnets for Wake-on-LAN.
func interfaces() []protocol.NetInterface {
	ifaces, err := stdnet.Interfaces()
	if err != nil {
		return nil
	}
	var out []protocol.NetInterface
	for _, i := range ifaces {
		if i.Flags&stdnet.FlagLoopback != 0 || len(i.HardwareAddr) != 6 || virtualInterface(i.Name) {
			continue
		}
		addrs, _ := i.Addrs()
		var cidrs []string
		for _, a := range addrs {
			if ipn, ok := a.(*stdnet.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				cidrs = append(cidrs, ipn.String())
			}
		}
		if len(cidrs) == 0 {
			continue
		}
		out = append(out, protocol.NetInterface{Name: i.Name, MAC: i.HardwareAddr.String(), Addrs: cidrs})
		if len(out) == 16 {
			break
		}
	}
	return out
}

// Sample reads current metrics. Rates cover the time since the previous call.
func (c *Collector) Sample() protocol.Metrics {
	now := time.Now()
	m := protocol.Metrics{Time: now.UnixMilli()}
	elapsed := now.Sub(c.prevTime).Seconds()
	haveRates := !c.prevTime.IsZero() && elapsed > 0

	if t, err := cpu.Times(false); err == nil && len(t) > 0 {
		if c.prevCPU != nil {
			m.CPU = cpuBusy(*c.prevCPU, t[0])
		}
		c.prevCPU = &t[0]
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemTotal = vm.Total
		if vm.Available < vm.Total {
			m.MemUsed = vm.Total - vm.Available
		}
	}
	if sw, err := mem.SwapMemory(); err == nil {
		m.SwapTotal, m.SwapUsed = sw.Total, sw.Used
	}

	m.Filesystems = c.filesystems()
	for _, fs := range m.Filesystems {
		if fs.Mount == c.primaryMount {
			m.DiskUsed, m.DiskTotal = fs.Used, fs.Total
			break
		}
	}

	netNow := netCounters()
	diskNow := diskCounters()
	if haveRates {
		m.NetRx = rate(c.prevNet.a, netNow.a, elapsed)
		m.NetTx = rate(c.prevNet.b, netNow.b, elapsed)
		m.DiskRead = rate(c.prevDisk.a, diskNow.a, elapsed)
		m.DiskWrite = rate(c.prevDisk.b, diskNow.b, elapsed)
	}
	c.prevNet, c.prevDisk = netNow, diskNow

	if runtime.GOOS != "windows" {
		if l, err := load.Avg(); err == nil {
			m.Load1, m.Load5, m.Load15 = l.Load1, l.Load5, l.Load15
		}
	}
	m.Uptime, _ = host.Uptime()
	m.Containers = c.docker.containers()

	c.prevTime = now
	return m
}

// cpuBusy mirrors gopsutil's percent calculation: guest time is already part of user time on Linux.
func cpuBusy(a, b cpu.TimesStat) float64 {
	total := func(t cpu.TimesStat) float64 {
		sum := t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
		if runtime.GOOS != "linux" {
			sum += t.Guest + t.GuestNice
		}
		return sum
	}
	ta, tb := total(a), total(b)
	busyA, busyB := ta-a.Idle-a.Iowait, tb-b.Idle-b.Iowait
	if tb <= ta || busyB <= busyA {
		return 0
	}
	return min(100, (busyB-busyA)/(tb-ta)*100)
}

func rate(prev, cur uint64, seconds float64) float64 {
	if cur < prev { // counter reset (reboot, interface re-created)
		return 0
	}
	return float64(cur-prev) / seconds
}

func (c *Collector) filesystems() []protocol.Filesystem {
	// Partitions may return partial results together with warnings; use whatever it found.
	parts, _ := disk.Partitions(false)
	// The primary mount goes first so it wins the de-duplication below.
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].Mountpoint == c.primaryMount && parts[j].Mountpoint != c.primaryMount
	})
	seen := map[string]bool{}
	var out []protocol.Filesystem
	for _, p := range parts {
		if p.Mountpoint == "" || seen[p.Device] || !keepFilesystem(p) {
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		// APFS volumes in one container (/, /Volumes/Recovery, ...) report the same
		// size and free space; list the container once.
		if runtime.GOOS == "darwin" {
			key := fmt.Sprint("apfs:", u.Total, ":", u.Free)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		seen[p.Device] = true
		// Total-Free rather than Used: on macOS "/" is a sealed system volume whose Used
		// excludes user data, while Free is the whole APFS container's free space.
		used := uint64(0)
		if u.Free < u.Total {
			used = u.Total - u.Free
		}
		out = append(out, protocol.Filesystem{Mount: p.Mountpoint, Device: p.Device, Type: p.Fstype, Used: used, Total: u.Total})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Mount == c.primaryMount) != (out[j].Mount == c.primaryMount) {
			return out[i].Mount == c.primaryMount
		}
		return out[i].Mount < out[j].Mount
	})
	if len(out) > maxFilesystems {
		out = out[:maxFilesystems]
	}
	return out
}

func primaryMount() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("SystemDrive"); d != "" {
			return strings.TrimSuffix(d, `\`)
		}
		return "C:"
	}
	return "/"
}

func netCounters() counters {
	var c counters
	stats, err := net.IOCounters(true)
	if err != nil {
		return c
	}
	for _, s := range stats {
		if virtualInterface(s.Name) {
			continue
		}
		c.a += s.BytesRecv
		c.b += s.BytesSent
	}
	return c
}

func diskCounters() counters {
	var c counters
	stats, err := disk.IOCounters()
	if err != nil {
		return c
	}
	for name, s := range stats {
		if !physicalDisk(name) {
			continue
		}
		c.a += s.ReadBytes
		c.b += s.WriteBytes
	}
	return c
}
