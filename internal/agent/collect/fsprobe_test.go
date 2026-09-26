package collect

import (
	"sync/atomic"
	"testing"
	"time"
)

// A filesystem call that hangs (a dead NFS server) must not stall samples, and
// must not pile up a new stuck goroutine every sample either.
func TestHungFilesystemCallsDoNotStallSamples(t *testing.T) {
	defer func(d time.Duration) { slowFSTimeout = d }(slowFSTimeout)
	slowFSTimeout = 50 * time.Millisecond

	p := newFSProbe()
	release := make(chan struct{})
	var calls atomic.Int32
	hang := func() {
		calls.Add(1)
		<-release
	}

	start := time.Now()
	p.run("usage:/mnt/nfs", hang)
	p.run("usage:/mnt/nfs", hang) // still stuck: not started again, returns at once
	if d := time.Since(start); d > time.Second {
		t.Fatalf("hung call stalled the sample for %s", d)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("hung call started %d times, want 1", n)
	}
	p.run("usage:/", func() { calls.Add(1) }) // other mounts are unaffected
	if n := calls.Load(); n != 2 {
		t.Fatalf("other mount was not read: %d calls", n)
	}

	close(release) // the server is back
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		running := p.running["usage:/mnt/nfs"]
		p.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished call still marked as running")
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.run("usage:/mnt/nfs", func() { calls.Add(1) })
	if n := calls.Load(); n != 3 {
		t.Fatalf("mount was not read again after it recovered: %d calls", n)
	}
}
