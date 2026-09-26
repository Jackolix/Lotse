package collect

import (
	"cmp"
	"errors"
	"os"
	"runtime"
	"slices"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/Jackolix/Lotse/internal/protocol"
)

// processSample is how long CPU time is measured for the process list.
const processSample = 500 * time.Millisecond

// Processes returns the busiest processes by CPU, then memory. It samples CPU
// time twice, so it takes about half a second; it only runs when someone looks.
// Command lines can contain secrets, so they are only included when asked for.
func Processes(limit int, withCommands bool) (protocol.ProcessList, error) {
	procs, err := process.Processes()
	if err != nil {
		return protocol.ProcessList{}, err
	}
	before := make(map[int32]float64, len(procs))
	for _, p := range procs {
		if t, err := p.Times(); err == nil {
			before[p.Pid] = t.User + t.System
		}
	}
	start := time.Now()
	time.Sleep(processSample)
	elapsed := time.Since(start).Seconds()
	cores := float64(runtime.NumCPU())

	type entry struct {
		p   *process.Process
		out protocol.Process
	}
	list := make([]entry, 0, len(procs))
	for _, p := range procs {
		prev, ok := before[p.Pid]
		t, err := p.Times()
		if !ok || err != nil { // started or exited meanwhile
			continue
		}
		e := entry{p: p, out: protocol.Process{PID: p.Pid, CPU: max(0, (t.User+t.System-prev)/elapsed/cores*100)}}
		if mi, err := p.MemoryInfo(); err == nil {
			e.out.Mem = mi.RSS
		}
		list = append(list, e)
	}
	slices.SortFunc(list, func(a, b entry) int {
		if c := cmp.Compare(b.out.CPU, a.out.CPU); c != 0 {
			return c
		}
		return cmp.Compare(b.out.Mem, a.out.Mem)
	})
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}

	// Names, users and command lines are slower to read; only fetch them for the shown rows.
	out := make([]protocol.Process, len(list))
	for i, e := range list {
		p := e.out
		p.Name, _ = e.p.Name()
		p.User, _ = e.p.Username()
		if ms, err := e.p.CreateTime(); err == nil {
			p.Started = ms / 1000
		}
		if withCommands {
			if cmd, err := e.p.Cmdline(); err == nil {
				if len(cmd) > 300 {
					n := 300
					for n > 0 && !utf8.RuneStart(cmd[n]) { // don't split a character
						n--
					}
					cmd = cmd[:n] + "…"
				}
				p.Command = cmd
			}
		}
		out[i] = p
	}
	return protocol.ProcessList{Processes: out, Total: total}, nil
}

// Signal stops a process. Windows has no SIGTERM, so "terminate" kills there too.
func Signal(pid int32, signal string) error {
	if pid <= 1 {
		return errors.New("refusing to signal PID 0 or 1")
	}
	if int(pid) == os.Getpid() {
		return errors.New("refusing to stop the agent itself")
	}
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	switch signal {
	case "kill":
		return p.Kill()
	case "terminate":
		if runtime.GOOS == "windows" {
			return p.Kill()
		}
		return p.Signal(syscall.SIGTERM)
	}
	return errors.New("unknown signal")
}
