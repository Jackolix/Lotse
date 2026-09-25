package hub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// waitRun polls a run until it has finished and returns it.
func waitRun(t *testing.T, srvURL, cookie string, id int64) runDTO {
	t.Helper()
	var run runDTO
	waitFor(t, "script run", 20*time.Second, func() bool {
		_, raw := authedGet(t, fmt.Sprintf("%s/api/runs/%d", srvURL, id), cookie)
		run = runDTO{}
		json.Unmarshal(raw, &run)
		return run.ScriptRun != nil && run.FinishedAt != nil
	})
	return run
}

func targetOf(t *testing.T, run runDTO, systemID int64) *runTarget {
	t.Helper()
	for _, tg := range run.Targets {
		if tg.SystemID == systemID {
			return tg
		}
	}
	t.Fatalf("no result for system %d in %+v", systemID, run.Targets)
	return nil
}

func TestScriptRunsOnSystems(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	locked := enroll(t, h, srv, false)
	cookie, _ := sessionFor(t, h, "otto", store.RoleOperator, true)

	status, body := call(t, "POST", srv.URL+"/api/scripts", cookie, map[string]any{
		"name": "Greet", "shell": "sh", "timeout": 30,
		"content": "echo hello-$((6*7))\necho oops >&2\nexit 3\n",
	})
	if status != http.StatusOK {
		t.Fatalf("save script = %d %v", status, body)
	}
	scriptID := int64(body["id"].(float64))

	plain, _ := sessionFor(t, h, "otto", store.RoleOperator, false)
	if status, body := call(t, "POST", srv.URL+"/api/runs", plain, map[string]any{"script_id": scriptID, "systems": []int64{sys.ID}}); status != http.StatusForbidden || body["reauth_required"] != true {
		t.Fatalf("run without re-authentication = %d %v", status, body)
	}
	status, body = call(t, "POST", srv.URL+"/api/runs", cookie, map[string]any{"script_id": scriptID, "systems": []int64{sys.ID, locked.ID}})
	if status != http.StatusOK {
		t.Fatalf("run = %d %v", status, body)
	}
	runID := int64(body["id"].(float64))

	// The event stream starts with the full run and ends with "done".
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/runs/%d/events", srv.URL, runID), nil)
	req.Header.Set("Cookie", "session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			events = append(events, name)
		}
	}
	resp.Body.Close()
	if len(events) < 2 || events[0] != "run" || events[len(events)-1] != "done" {
		t.Errorf("events = %v", events)
	}

	run := waitRun(t, srv.URL, cookie, runID)
	got := targetOf(t, run, sys.ID)
	if got.Status != "failed" || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Errorf("result = %+v", got)
	}
	if !strings.Contains(got.Output, "hello-42") || !strings.Contains(got.Output, "oops") {
		t.Errorf("output = %q", got.Output)
	}
	if skipped := targetOf(t, run, locked.ID); skipped.Status != "skipped" || !strings.Contains(skipped.Error, "--allow-shell") {
		t.Errorf("agent without --allow-shell: %+v", skipped)
	}
	if bad := targetOf(t, run, sys.ID); bad.SystemName != sys.Name {
		t.Errorf("system name = %q", bad.SystemName)
	}

	// Windows-only shells are skipped on other systems.
	_, body = call(t, "POST", srv.URL+"/api/runs", cookie, map[string]any{"shell": "cmd", "content": "dir", "timeout": 10, "systems": []int64{sys.ID}})
	if res := targetOf(t, waitRun(t, srv.URL, cookie, int64(body["id"].(float64))), sys.ID); res.Status != "skipped" {
		t.Errorf("cmd on Linux: %+v", res)
	}

	// The time limit stops the script and everything it started.
	_, body = call(t, "POST", srv.URL+"/api/runs", cookie, map[string]any{"shell": "sh", "content": "sleep 30 & sleep 30", "timeout": 1, "systems": []int64{sys.ID}})
	res := targetOf(t, waitRun(t, srv.URL, cookie, int64(body["id"].(float64))), sys.ID)
	if res.ExitCode == nil || *res.ExitCode != 124 || !strings.Contains(res.Output, "time limit") {
		t.Errorf("timed out script: %+v", res)
	}

	// Cancelling stops a running script.
	_, body = call(t, "POST", srv.URL+"/api/runs", cookie, map[string]any{"shell": "sh", "content": "echo started; sleep 30", "timeout": 60, "systems": []int64{sys.ID}})
	id := int64(body["id"].(float64))
	waitFor(t, "script start", 10*time.Second, func() bool {
		_, raw := authedGet(t, fmt.Sprintf("%s/api/runs/%d", srv.URL, id), cookie)
		return strings.Contains(string(raw), "started")
	})
	if status, _ := call(t, "POST", fmt.Sprintf("%s/api/runs/%d/cancel", srv.URL, id), cookie, nil); status != http.StatusNoContent {
		t.Fatalf("cancel = %d", status)
	}
	if res := targetOf(t, waitRun(t, srv.URL, cookie, id), sys.ID); res.Status != "canceled" {
		t.Errorf("canceled run: %+v", res)
	}

	entries, _ := h.store.Audit(0, 100)
	var ran int
	for _, e := range entries {
		if e.Action == "script_run" && e.Username == "otto" {
			ran++
		}
	}
	if ran < 3 {
		t.Errorf("script runs in the audit log: %d", ran)
	}
}

func TestInterruptedRunsAreClosed(t *testing.T) {
	h, _ := newTestHub(t)
	targets := `[{"system_id":1,"system_name":"a","status":"running","exit_code":null,"output":""}]`
	rec := &store.ScriptRun{Name: "x", Shell: "sh", Content: "true", Timeout: 5, Username: "u", StartedAt: 1, Targets: targets}
	if err := h.store.CreateRun(rec); err != nil {
		t.Fatal(err)
	}
	if err := h.finishInterruptedRuns(); err != nil {
		t.Fatal(err)
	}
	got, _ := h.store.Run(rec.ID)
	if got.FinishedAt == nil || !strings.Contains(got.Targets, "the hub restarted") {
		t.Errorf("run after restart: %+v", got)
	}
}
