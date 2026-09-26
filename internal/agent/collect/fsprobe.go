package collect

import (
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// slowFSTimeout is how long a sample waits for one filesystem call. A variable so
// tests can shorten it.
var slowFSTimeout = time.Second

// fsProbe reads filesystems without letting an unreachable network share stall the
// whole sample: statfs on a hard-mounted NFS export whose server is gone does not
// return until the server is back, and on macOS listing the mounts asks every
// filesystem as well. Calls run in the background with a deadline, results that
// arrive late are used by the next sample, and a call still stuck from an earlier
// sample is not started again.
type fsProbe struct {
	mu      sync.Mutex
	running map[string]bool            // calls that have not returned yet
	usages  map[string]*disk.UsageStat // newest result per mount; nil after an error
	parts   []disk.PartitionStat
}

func newFSProbe() *fsProbe {
	return &fsProbe{running: map[string]bool{}, usages: map[string]*disk.UsageStat{}}
}

// partitions lists the mounted filesystems, or the previous list while that hangs.
func (p *fsProbe) partitions() []disk.PartitionStat {
	p.run("partitions", func() {
		parts, _ := disk.Partitions(false) // partial results come with warnings; use what it found
		p.mu.Lock()
		p.parts = parts
		p.mu.Unlock()
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.parts
}

// usage returns the newest known usage of a mount, or nil if there is none.
func (p *fsProbe) usage(mount string) *disk.UsageStat {
	p.run("usage:"+mount, func() {
		u, err := disk.Usage(mount)
		if err != nil {
			u = nil
		}
		p.mu.Lock()
		p.usages[mount] = u
		p.mu.Unlock()
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.usages[mount]
}

// forget drops cached usage of mounts that are gone.
func (p *fsProbe) forget(keep map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for m := range p.usages {
		if !keep[m] && !p.running["usage:"+m] {
			delete(p.usages, m)
		}
	}
}

// run calls fn and waits up to slowFSTimeout for it to return.
func (p *fsProbe) run(key string, fn func()) {
	p.mu.Lock()
	if p.running[key] {
		p.mu.Unlock()
		return
	}
	p.running[key] = true
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
		p.mu.Lock()
		delete(p.running, key)
		p.mu.Unlock()
	}()
	timer := time.NewTimer(slowFSTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
