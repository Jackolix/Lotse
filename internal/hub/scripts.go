package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// Saved scripts run on many systems at once. Each system runs the script in its own
// session channel; output streams to anyone watching the run and is stored with the
// results once all systems are done.

const (
	maxScriptSize   = 256 << 10
	maxRunOutput    = 128 << 10 // per system; older output is dropped beyond this
	maxRunTargets   = 500
	runParallel     = 16
	maxRunTimeout   = 24 * 60 * 60
	runKeepInMemory = time.Minute // after finishing, for late viewers
)

var scriptShells = map[string]string{"sh": "sh", "bash": "Bash", "powershell": "PowerShell", "cmd": "cmd"}

// shellRunsOn reports whether a shell exists on an OS (PowerShell 7 also runs on
// Linux and macOS, but usually is not installed there).
func shellRunsOn(shell, goos string) bool {
	switch shell {
	case "sh", "bash":
		return goos != "windows"
	case "cmd":
		return goos == "windows"
	}
	return true
}

// ---- saved scripts ----

func (h *Hub) getScripts(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	list, err := h.store.Scripts()
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func validateScript(sc *store.Script) string {
	sc.Name = strings.TrimSpace(sc.Name)
	sc.Description = strings.TrimSpace(sc.Description)
	switch {
	case sc.Name == "" || utf8.RuneCountInString(sc.Name) > maxNameLen:
		return "name must be 1-64 characters"
	case utf8.RuneCountInString(sc.Description) > 500:
		return "the description must be at most 500 characters"
	case scriptShells[sc.Shell] == "":
		return "shell must be sh, bash, powershell or cmd"
	case strings.TrimSpace(sc.Content) == "":
		return "the script is empty"
	case len(sc.Content) > maxScriptSize:
		return "the script must be at most 256 KiB"
	case sc.Timeout < 1 || sc.Timeout > maxRunTimeout:
		return "the time limit must be between 1 second and 24 hours"
	}
	return ""
}

func (h *Hub) saveScript(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var sc store.Script
	if !readJSON(w, r, &sc) {
		return
	}
	sc.ID = 0
	if idStr := r.PathValue("id"); idStr != "" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		old, err := h.store.Script(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		sc.ID, sc.CreatedBy, sc.CreatedAt = id, old.CreatedBy, old.CreatedAt
	} else {
		sc.CreatedBy = s.Username
	}
	if msg := validateScript(&sc); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.store.SaveScript(&sc); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "script_saved", nil, fmt.Sprintf("%q (%s)", sc.Name, scriptShells[sc.Shell]))
	writeJSON(w, http.StatusOK, sc)
}

func (h *Hub) deleteScript(w http.ResponseWriter, r *http.Request, s *store.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sc, err := h.store.Script(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if err := h.store.DeleteScript(id); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "script_deleted", nil, fmt.Sprintf("%q", sc.Name))
	w.WriteHeader(http.StatusNoContent)
}

// ---- runs ----

// runTarget is the result on one system.
type runTarget struct {
	SystemID   int64  `json:"system_id"`
	SystemName string `json:"system_name"`
	Status     string `json:"status"` // pending, running, done, failed, skipped, canceled
	ExitCode   *int   `json:"exit_code"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated,omitempty"` // the start of the output was dropped
	StartedAt  int64  `json:"started_at,omitempty"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

type runDTO struct {
	*store.ScriptRun
	Targets []*runTarget `json:"targets"`
}

// runEvent is sent to viewers of a running run: new output or a status change.
type runEvent struct {
	name string
	data any
}

// activeRun is a run in progress, kept in memory while its systems report.
type activeRun struct {
	rec    *store.ScriptRun
	cancel context.CancelFunc

	mu      sync.Mutex
	targets []*runTarget
	output  [][]byte // per target, bounded by maxRunOutput
	subs    map[chan runEvent]struct{}
	done    bool
}

func (ar *activeRun) publishLocked(ev runEvent) {
	for ch := range ar.subs {
		select {
		case ch <- ev:
		default: // a slow viewer misses output; it can reload the run
		}
	}
}

func (ar *activeRun) snapshot() runDTO {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	return ar.snapshotLocked()
}

func (ar *activeRun) snapshotLocked() runDTO {
	out := make([]*runTarget, len(ar.targets))
	for i, t := range ar.targets {
		c := *t
		c.Output = string(ar.output[i])
		out[i] = &c
	}
	rec := *ar.rec
	return runDTO{&rec, out}
}

func (ar *activeRun) update(i int, fn func(t *runTarget)) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	fn(ar.targets[i])
	c := *ar.targets[i]
	c.Output = ""
	ar.publishLocked(runEvent{"target", &c})
}

// write appends output of target i, keeping the newest maxRunOutput bytes.
func (ar *activeRun) write(i int, p []byte) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	buf := append(ar.output[i], p...)
	if len(buf) > maxRunOutput {
		buf = append(buf[:0:0], buf[len(buf)-maxRunOutput:]...)
		ar.targets[i].Truncated = true
	}
	ar.output[i] = buf
	ar.publishLocked(runEvent{"output", map[string]any{"system_id": ar.targets[i].SystemID, "data": string(p)}})
}

type runRequest struct {
	ScriptID *int64  `json:"script_id"`
	Name     string  `json:"name"`
	Shell    string  `json:"shell"`
	Content  string  `json:"content"`
	Timeout  int     `json:"timeout"`
	Systems  []int64 `json:"systems"`
}

// postRun starts a saved script (script_id) or a one-off command on systems.
func (h *Hub) postRun(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var req runRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !requireElevated(w, s) {
		return
	}
	sc := &store.Script{Name: req.Name, Shell: req.Shell, Content: req.Content, Timeout: req.Timeout}
	if req.ScriptID != nil {
		saved, err := h.store.Script(*req.ScriptID)
		if err != nil {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		sc = saved
	} else if strings.TrimSpace(sc.Name) == "" {
		sc.Name = "Command"
	}
	if msg := validateScript(sc); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	slices.Sort(req.Systems)
	req.Systems = slices.Compact(req.Systems)
	if len(req.Systems) == 0 || len(req.Systems) > maxRunTargets {
		writeError(w, http.StatusBadRequest, "choose at least one system")
		return
	}
	var targets []*runTarget
	for _, id := range req.Systems {
		sys, err := h.store.System(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "a selected system does not exist")
			return
		}
		targets = append(targets, &runTarget{SystemID: sys.ID, SystemName: sys.Name, Status: "pending"})
	}

	raw, _ := json.Marshal(targets)
	rec := &store.ScriptRun{ScriptID: req.ScriptID, Name: sc.Name, Shell: sc.Shell, Content: sc.Content, Timeout: sc.Timeout,
		Username: s.Username, StartedAt: time.Now().Unix(), Targets: string(raw)}
	if err := h.store.CreateRun(rec); err != nil {
		h.internalError(w, err)
		return
	}
	// Runs outlive the request; they stop with the hub.
	ctx, cancel := context.WithCancel(h.ctx)
	ar := &activeRun{rec: rec, cancel: cancel, targets: targets, output: make([][]byte, len(targets)), subs: map[chan runEvent]struct{}{}}
	h.mu.Lock()
	h.runs[rec.ID] = ar
	h.mu.Unlock()
	h.runWG.Add(1)
	go h.execute(ctx, ar, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]int64{"id": rec.ID})
}

// execute runs the script on every target, a limited number at a time, then stores
// the results.
func (h *Hub) execute(ctx context.Context, ar *activeRun, remote string) {
	defer h.runWG.Done()
	defer ar.cancel()
	sem := make(chan struct{}, runParallel)
	var wg sync.WaitGroup
	for i := range ar.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
			}
			h.runOn(ctx, ar, i)
		}()
	}
	wg.Wait()

	finished := time.Now().Unix()
	dto := ar.snapshot()
	raw, _ := json.Marshal(dto.Targets)
	if err := h.store.FinishRun(ar.rec.ID, finished, string(raw)); err != nil {
		h.log.Error("storing script results failed", "run", ar.rec.ID, "err", err)
	}
	for _, t := range dto.Targets {
		detail := fmt.Sprintf("%q: %s", ar.rec.Name, t.Status)
		if t.ExitCode != nil {
			detail = fmt.Sprintf("%q: exit status %d", ar.rec.Name, *t.ExitCode)
		}
		if t.Error != "" {
			detail += " (" + t.Error + ")"
		}
		if t.StartedAt > 0 {
			detail += fmt.Sprintf(" after %s", time.Duration(t.FinishedAt-t.StartedAt)*time.Second)
		}
		id := t.SystemID
		e := store.AuditEntry{Username: ar.rec.Username, Action: "script_run", SystemID: &id, SystemName: t.SystemName,
			Remote: remote, Detail: detail}
		if err := h.store.AddAudit(e); err != nil {
			h.log.Error("writing audit log failed", "err", err)
		}
	}

	ar.mu.Lock()
	ar.rec.FinishedAt = &finished
	ar.done = true
	ar.publishLocked(runEvent{"done", map[string]int64{"finished_at": finished}})
	for ch := range ar.subs {
		close(ch)
	}
	ar.subs = nil
	ar.mu.Unlock()
	time.AfterFunc(runKeepInMemory, func() {
		h.mu.Lock()
		delete(h.runs, ar.rec.ID)
		h.mu.Unlock()
	})
}

// runOn runs the script on one system.
func (h *Hub) runOn(ctx context.Context, ar *activeRun, i int) {
	finish := func(status, msg string, code *int) {
		ar.update(i, func(t *runTarget) {
			t.Status, t.Error, t.ExitCode = status, msg, code
			t.FinishedAt = time.Now().Unix()
		})
	}
	if ctx.Err() != nil {
		finish("canceled", "", nil)
		return
	}
	id := ar.targets[i].SystemID
	h.mu.Lock()
	ac := h.agents[id]
	h.mu.Unlock()
	switch {
	case ac == nil:
		finish("skipped", "the system is offline", nil)
		return
	case !ac.has(protocol.FeatureShell):
		finish("skipped", "scripts are disabled on this machine (install the agent with --allow-shell)", nil)
		return
	case !shellRunsOn(ar.rec.Shell, ac.info.OS):
		finish("skipped", fmt.Sprintf("%s scripts do not run on %s", scriptShells[ar.rec.Shell], osName(ac.info.OS)), nil)
		return
	}

	ch, reqs, err := ac.conn.OpenChannel("session", nil)
	if err != nil {
		finish("failed", "the agent refused: "+channelError(err), nil)
		return
	}
	defer ch.Close()
	exit := make(chan int, 1)
	go func() {
		for req := range reqs {
			var st protocol.ExitStatus
			if req.Type == "exit-status" && ssh.Unmarshal(req.Payload, &st) == nil {
				select {
				case exit <- int(st.Status):
				default:
				}
			}
			req.Reply(false, nil)
		}
	}()
	payload, _ := json.Marshal(protocol.ScriptMsg{Shell: ar.rec.Shell, Script: ar.rec.Content, Timeout: ar.rec.Timeout})
	if ok, err := ch.SendRequest(protocol.ReqScript, true, payload); err != nil || !ok {
		finish("failed", "this agent cannot run scripts; update it", nil)
		return
	}
	ar.update(i, func(t *runTarget) {
		t.Status = "running"
		t.StartedAt = time.Now().Unix()
	})

	// The agent enforces the time limit; the hub gives up a little later in case the
	// agent itself hangs.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ar.rec.Timeout)*time.Second+30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { ch.Close() })
	defer stop()
	buf := make([]byte, 32<<10)
	for {
		n, err := ch.Read(buf)
		if n > 0 {
			ar.write(i, buf[:n])
		}
		if err != nil {
			break
		}
	}
	select {
	case code := <-exit:
		status := "done"
		if code != 0 {
			status = "failed"
		}
		finish(status, "", &code)
	case <-time.After(2 * time.Second):
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			finish("canceled", "stopped by a user", nil)
		case ctx.Err() != nil:
			finish("failed", "the agent did not finish in time", nil)
		default:
			finish("failed", "the connection to the agent was lost", nil)
		}
	}
}

func osName(goos string) string {
	return map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}[goos]
}

func (h *Hub) getRuns(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	recs, err := h.store.Runs(50)
	if err != nil {
		h.internalError(w, err)
		return
	}
	out := make([]runDTO, len(recs))
	for i, rec := range recs {
		dto := h.runDTO(rec)
		dto.Content = "" // the list only shows results
		for _, t := range dto.Targets {
			t.Output = ""
		}
		out[i] = dto
	}
	writeJSON(w, http.StatusOK, out)
}

// runDTO prefers the live state of a run in progress over the stored one.
func (h *Hub) runDTO(rec *store.ScriptRun) runDTO {
	h.mu.Lock()
	ar := h.runs[rec.ID]
	h.mu.Unlock()
	if ar != nil {
		return ar.snapshot()
	}
	dto := runDTO{ScriptRun: rec}
	_ = json.Unmarshal([]byte(rec.Targets), &dto.Targets)
	return dto
}

func (h *Hub) lookupRun(w http.ResponseWriter, r *http.Request) (*store.ScriptRun, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rec, err := h.store.Run(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "run not found")
		return nil, false
	}
	if err != nil {
		h.internalError(w, err)
		return nil, false
	}
	return rec, true
}

func (h *Hub) getRun(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	rec, ok := h.lookupRun(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.runDTO(rec))
}

// getRunEvents streams a run as server-sent events: "run" with the full state
// first, then "target" (status changes), "output" (new output) and "done".
func (h *Hub) getRunEvents(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	rec, ok := h.lookupRun(w, r)
	if !ok {
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(name string, data any) bool {
		payload, _ := json.Marshal(data)
		if _, err := io.WriteString(w, "event: "+name+"\ndata: "+string(payload)+"\n\n"); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	h.mu.Lock()
	ar := h.runs[rec.ID]
	h.mu.Unlock()
	if ar == nil {
		send("run", h.runDTO(rec))
		send("done", map[string]any{"finished_at": rec.FinishedAt})
		return
	}
	// Snapshot and subscribe at once, so output is neither missed nor sent twice.
	ch := make(chan runEvent, 256)
	ar.mu.Lock()
	finished := ar.done
	if !finished {
		ar.subs[ch] = struct{}{}
	}
	snap := ar.snapshotLocked()
	ar.mu.Unlock()
	if !send("run", snap) {
		return
	}
	if finished {
		send("done", map[string]any{"finished_at": ar.rec.FinishedAt})
		return
	}
	defer func() {
		ar.mu.Lock()
		if ar.subs != nil {
			delete(ar.subs, ch)
		}
		ar.mu.Unlock()
	}()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !send(ev.name, ev.data) {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (h *Hub) postRunCancel(w http.ResponseWriter, r *http.Request, s *store.Session) {
	rec, ok := h.lookupRun(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	ar := h.runs[rec.ID]
	h.mu.Unlock()
	if ar == nil || rec.FinishedAt != nil {
		writeError(w, http.StatusConflict, "the run has finished")
		return
	}
	ar.cancel()
	h.audit(r, s.Username, "script_canceled", nil, fmt.Sprintf("%q", rec.Name))
	w.WriteHeader(http.StatusNoContent)
}

// finishInterruptedRuns closes runs that were going when the hub stopped.
func (h *Hub) finishInterruptedRuns() error {
	recs, err := h.store.UnfinishedRuns()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		var targets []*runTarget
		_ = json.Unmarshal([]byte(rec.Targets), &targets)
		for _, t := range targets {
			if t.Status == "pending" || t.Status == "running" {
				t.Status, t.Error = "failed", "the hub restarted during the run"
			}
		}
		raw, _ := json.Marshal(targets)
		if err := h.store.FinishRun(rec.ID, rec.StartedAt, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
