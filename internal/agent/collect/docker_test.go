package collect

import (
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A fake Docker API on a Unix socket: one running and one stopped container,
// with counters that grow between calls.
func TestContainerStats(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	var calls atomic.Int64
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			w.Write([]byte(`[{"Id":"aaaaaaaaaaaaaaaa","Names":["/web"],"Image":"nginx","State":"running","Status":"Up 1 hour"},
				{"Id":"bbbbbbbbbbbbbbbb","Names":["/job"],"Image":"alpine","State":"exited","Status":"Exited (0)"}]`))
		case strings.HasPrefix(r.URL.Path, "/containers/aaaaaaaaaaaaaaaa/stats"):
			n := calls.Add(1)
			// Each call: +1s of container CPU, +4s of host CPU → 25 % of the machine.
			w.Write([]byte(strings.NewReplacer("CPU", itoa(n*1e9), "SYS", itoa(n*4e9), "RX", itoa(n*1000)).Replace(
				`{"cpu_stats":{"cpu_usage":{"total_usage":CPU},"system_cpu_usage":SYS},
				  "memory_stats":{"usage":300,"limit":1000,"stats":{"inactive_file":100}},
				  "networks":{"eth0":{"rx_bytes":RX,"tx_bytes":0}}}`)))
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "unix://"+sock)

	var d docker
	first := d.containers()
	if len(first) != 2 || first[0].Name != "web" || first[0].ID != "aaaaaaaaaaaa" || first[1].State != "exited" {
		t.Fatalf("containers: %+v", first)
	}
	if first[0].Mem != 200 || first[0].MemLimit != 1000 {
		t.Errorf("memory = %d of %d, want 200 of 1000 (without page cache)", first[0].Mem, first[0].MemLimit)
	}
	d.prevTime = d.prevTime.Add(-time.Second) // pretend a second passed
	second := d.containers()
	if cpu := second[0].CPU; cpu < 24.9 || cpu > 25.1 {
		t.Errorf("CPU = %.2f %%, want 25", cpu)
	}
	if second[0].NetRx < 900 {
		t.Errorf("rx rate = %.0f B/s, want about 1000", second[0].NetRx)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
