package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Jackolix/Lotse/internal/protocol"
)

// maxContainers bounds the per-sample container list.
const maxContainers = 64

// docker reads container stats from the Docker (or Podman) API socket. It keeps
// the previous counters to turn them into rates, like Collector does for the host.
type docker struct {
	client    *http.Client
	socket    string
	lastProbe time.Time
	prev      map[string]containerCounters
	prevTime  time.Time
}

type containerCounters struct {
	cpu, system uint64 // container and host CPU time, ns
	rx, tx      uint64
}

func dockerSockets() []string {
	var paths []string
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		paths = append(paths, strings.TrimPrefix(h, "unix://"))
	}
	paths = append(paths, "/var/run/docker.sock", "/run/podman/podman.sock")
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".docker/run/docker.sock"))
	}
	return paths
}

// available finds a socket, re-probing at most once a minute while none is found.
func (d *docker) available() bool {
	if runtime.GOOS == "windows" {
		return false // Docker Desktop's named pipe is not supported yet
	}
	if d.client != nil {
		return true
	}
	if time.Since(d.lastProbe) < time.Minute {
		return false
	}
	d.lastProbe = time.Now()
	for _, p := range dockerSockets() {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			socket := p
			d.socket = socket
			d.client = &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				}},
			}
			return true
		}
	}
	return false
}

func (d *docker) get(ctx context.Context, path string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker API %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type dockerContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

type dockerStats struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"cpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		Rx uint64 `json:"rx_bytes"`
		Tx uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// containers lists all containers with stats for the running ones, or nil when
// no container runtime is reachable.
func (d *docker) containers() []protocol.Container {
	if !d.available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var list []dockerContainer
	if err := d.get(ctx, "/containers/json?all=true", &list); err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "connect") {
			d.client = nil // the runtime went away; probe again later
		}
		return nil
	}
	if len(list) > maxContainers {
		list = list[:maxContainers]
	}

	now := time.Now()
	elapsed := now.Sub(d.prevTime).Seconds()
	counters := make(map[string]containerCounters, len(list))
	out := make([]protocol.Container, len(list))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // stats requests in parallel
	for i, c := range list {
		name := strings.TrimPrefix(firstOr(c.Names, c.ID), "/")
		out[i] = protocol.Container{ID: shortID(c.ID), Name: name, Image: c.Image, State: c.State, Status: c.Status}
		if c.State != "running" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var s dockerStats
			if err := d.get(ctx, "/containers/"+c.ID+"/stats?stream=false&one-shot=true", &s); err != nil {
				return
			}
			cur := containerCounters{cpu: s.CPU.Usage.Total, system: s.CPU.System}
			for _, n := range s.Networks {
				cur.rx += n.Rx
				cur.tx += n.Tx
			}
			mu.Lock()
			defer mu.Unlock()
			counters[c.ID] = cur
			ct := &out[i]
			// Memory without the page cache, as `docker stats` shows it.
			ct.Mem = s.Memory.Usage - min(s.Memory.Usage, s.Memory.Stats["inactive_file"]+s.Memory.Stats["total_inactive_file"])
			ct.MemLimit = s.Memory.Limit
			if prev, ok := d.prev[c.ID]; ok && elapsed > 0 {
				if cur.system > prev.system && cur.cpu >= prev.cpu {
					ct.CPU = float64(cur.cpu-prev.cpu) / float64(cur.system-prev.system) * 100
				}
				ct.NetRx = rate(prev.rx, cur.rx, elapsed)
				ct.NetTx = rate(prev.tx, cur.tx, elapsed)
			}
		}()
	}
	wg.Wait()
	d.prev, d.prevTime = counters, now
	return out
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
