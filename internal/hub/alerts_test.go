package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// recorder is a fake notification endpoint.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
	srv  *httptest.Server
}

func newRecorder(t *testing.T, status int) *recorder {
	rec := &recorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r)
		rec.body = append(rec.body, string(b))
		rec.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (rec *recorder) bodies() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.body...)
}

// alertHub is a test hub without the seeded default rules, plus one system.
func alertHub(t *testing.T) (*Hub, *store.System) {
	h, _ := newTestHub(t)
	rules, _ := h.store.AlertRules()
	for _, r := range rules {
		h.store.DeleteAlertRule(r.ID)
	}
	sys, err := h.store.CreateSystem("web-1", "SHA256:web1", "{}", "test")
	if err != nil {
		t.Fatal(err)
	}
	return h, sys
}

func addRule(t *testing.T, h *Hub, r store.AlertRule) *store.AlertRule {
	t.Helper()
	r.Enabled = true
	if msg := h.validateRule(&r); msg != "" {
		t.Fatal(msg)
	}
	if err := h.store.SaveAlertRule(&r); err != nil {
		t.Fatal(err)
	}
	h.resetAlerts(r.ID, 0)
	return &r
}

func addWebhook(t *testing.T, h *Hub, url string) int64 {
	t.Helper()
	cfg, _ := json.Marshal(notifierConfig{URL: url})
	n := &store.Notifier{Name: "hook", Type: "webhook", Config: string(cfg), Enabled: true}
	if err := h.store.SaveNotifier(n); err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func activeCount(t *testing.T, h *Hub) int {
	a, err := h.store.ActiveAlerts()
	if err != nil {
		t.Fatal(err)
	}
	return len(a)
}

func TestThresholdAlertFiresAndResolves(t *testing.T) {
	h, sys := alertHub(t)
	hook := newRecorder(t, http.StatusOK)
	addRule(t, h, store.AlertRule{Name: "Busy", Metric: "cpu", Threshold: 50, Duration: 60, Notifiers: []int64{addWebhook(t, h, hook.srv.URL)}})

	t0 := time.Now()
	busy := &protocol.Metrics{CPU: 80}
	h.evaluateMetrics(sys.ID, busy, t0)
	h.evaluateMetrics(sys.ID, busy, t0.Add(59*time.Second))
	if activeCount(t, h) != 0 {
		t.Fatal("fired before the duration passed")
	}
	h.evaluateMetrics(sys.ID, busy, t0.Add(61*time.Second))
	if activeCount(t, h) != 1 {
		t.Fatal("did not fire after the duration")
	}
	h.evaluateMetrics(sys.ID, busy, t0.Add(120*time.Second))
	if activeCount(t, h) != 1 {
		t.Fatal("fired twice")
	}

	// Back to normal: resolves only after staying there for alertClearDelay.
	calm := &protocol.Metrics{CPU: 10}
	t1 := t0.Add(130 * time.Second)
	h.evaluateMetrics(sys.ID, calm, t1)
	h.evaluateMetrics(sys.ID, busy, t1.Add(10*time.Second)) // a spike keeps it firing
	h.evaluateMetrics(sys.ID, calm, t1.Add(20*time.Second))
	h.evaluateMetrics(sys.ID, calm, t1.Add(70*time.Second))
	if activeCount(t, h) != 1 {
		t.Fatal("resolved before the clear delay")
	}
	h.evaluateMetrics(sys.ID, calm, t1.Add(81*time.Second))
	if activeCount(t, h) != 0 {
		t.Fatal("did not resolve")
	}

	waitFor(t, "two notifications", 5*time.Second, func() bool { return len(hook.bodies()) == 2 })
	var firing, resolved map[string]any
	json.Unmarshal([]byte(hook.bodies()[0]), &firing)
	json.Unmarshal([]byte(hook.bodies()[1]), &resolved)
	if firing["status"] == resolved["status"] {
		// Deliveries run concurrently; order them.
		firing, resolved = resolved, firing
	}
	if firing["status"] != "firing" || firing["system"] != "web-1" || firing["value"].(float64) != 80 {
		t.Errorf("firing payload: %v", firing)
	}
	if resolved["status"] != "resolved" || !strings.Contains(resolved["title"].(string), "back to normal") {
		t.Errorf("resolved payload: %v", resolved)
	}
}

func TestFiringAlertSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	h, err := New(Config{DataDir: dir}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	sys, _ := h.store.CreateSystem("db-1", "SHA256:db1", "{}", "test")
	rule := &store.AlertRule{Name: "Memory", Metric: "memory", Threshold: 50, Duration: 0, Enabled: true}
	h.store.SaveAlertRule(rule)
	h.resetAlerts(rule.ID, 0)
	full := &protocol.Metrics{MemUsed: 9, MemTotal: 10}
	h.evaluateMetrics(sys.ID, full, time.Now())
	if activeCount(t, h) != 1 {
		t.Fatal("did not fire")
	}
	h.store.Close()

	h2, err := New(Config{DataDir: dir}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.store.Close()
	h2.evaluateMetrics(sys.ID, full, time.Now())
	if activeCount(t, h2) != 1 {
		t.Fatal("a restart created a duplicate alert")
	}
}

func TestOfflineAlert(t *testing.T) {
	h, sys := alertHub(t)
	addRule(t, h, store.AlertRule{Metric: "offline", Duration: 120})
	h.alerts.started = time.Now().Add(-time.Minute)

	h.evaluateOffline(time.Now())
	if activeCount(t, h) != 0 {
		t.Fatal("fired during the grace period after start")
	}
	h.evaluateOffline(time.Now().Add(90 * time.Second))
	if activeCount(t, h) != 1 {
		t.Fatal("offline alert did not fire")
	}

	// Reconnecting resolves it at once.
	h.mu.Lock()
	h.agents[sys.ID] = &agentConn{id: sys.ID}
	h.mu.Unlock()
	h.evaluateOffline(time.Now().Add(100 * time.Second))
	if activeCount(t, h) != 0 {
		t.Fatal("offline alert did not resolve when the agent came back")
	}
}

func TestDeletingSystemResolvesItsAlerts(t *testing.T) {
	h, sys := alertHub(t)
	addRule(t, h, store.AlertRule{Metric: "disk", Threshold: 50, Duration: 0})
	h.evaluateMetrics(sys.ID, &protocol.Metrics{DiskUsed: 9, DiskTotal: 10}, time.Now())
	if activeCount(t, h) != 1 {
		t.Fatal("did not fire")
	}
	h.resetAlerts(0, sys.ID)
	if activeCount(t, h) != 0 {
		t.Fatal("alert of a deleted system stayed active")
	}
}

func TestRuleValidation(t *testing.T) {
	h, _ := alertHub(t)
	for _, bad := range []store.AlertRule{
		{Metric: "nope", Threshold: 1},
		{Metric: "cpu", Threshold: 150, Duration: 60},
		{Metric: "load", Threshold: 0, Duration: 60},
		{Metric: "offline", Duration: 5},
		{Metric: "cpu", Threshold: 80, Duration: 60, Notifiers: []int64{999}},
	} {
		if msg := h.validateRule(&bad); msg == "" {
			t.Errorf("accepted %+v", bad)
		}
	}
	ok := store.AlertRule{Metric: "cpu", Threshold: 80, Duration: 60}
	if msg := h.validateRule(&ok); msg != "" || ok.Name != "CPU" {
		t.Errorf("valid rule rejected (%q) or unnamed (%q)", msg, ok.Name)
	}
}

func TestChannelFormats(t *testing.T) {
	n := notification{Title: "web-1: CPU at 95%", Message: "above 90%", Firing: true, URL: "http://hub/systems/1", Time: time.Now()}

	ntfy := newRecorder(t, http.StatusOK)
	if err := send(t.Context(), "ntfy", notifierConfig{URL: ntfy.srv.URL + "/alerts", Token: "tk"}, n); err != nil {
		t.Fatal(err)
	}
	r := ntfy.reqs[0]
	if r.URL.Path != "/alerts" || r.Header.Get("Title") != n.Title || r.Header.Get("Priority") != "high" ||
		r.Header.Get("Authorization") != "Bearer tk" || r.Header.Get("Click") != n.URL || ntfy.body[0] != n.Message {
		t.Errorf("ntfy request: %v %q", r.Header, ntfy.body[0])
	}

	discord := newRecorder(t, http.StatusNoContent)
	if err := send(t.Context(), "discord", notifierConfig{URL: discord.srv.URL}, n); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(discord.body[0], `"embeds"`) || !strings.Contains(discord.body[0], "CPU at 95%") {
		t.Errorf("discord body: %s", discord.body[0])
	}

	tg := newRecorder(t, http.StatusOK)
	telegramAPI = tg.srv.URL
	defer func() { telegramAPI = "https://api.telegram.org" }()
	if err := send(t.Context(), "telegram", notifierConfig{Token: "123:abc", ChatID: "42"}, n); err != nil {
		t.Fatal(err)
	}
	if tg.reqs[0].URL.Path != "/bot123:abc/sendMessage" || !strings.Contains(tg.body[0], `"chat_id":"42"`) {
		t.Errorf("telegram request: %s %s", tg.reqs[0].URL.Path, tg.body[0])
	}

	broken := newRecorder(t, http.StatusInternalServerError)
	if err := send(t.Context(), "slack", notifierConfig{URL: broken.srv.URL}, n); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing endpoint should report its status, got %v", err)
	}
}

func TestNotifierSecretsAreMasked(t *testing.T) {
	h, srv := newTestHub(t)
	newSession(t, h, false)
	c := newClient(t, srv.URL)
	c.post("/api/login", map[string]string{"username": "admin", "password": testPassword})

	status, body := c.post("/api/notifiers", map[string]any{"name": "phone", "type": "telegram", "enabled": true,
		"config": map[string]string{"token": "123:secret", "chat_id": "42"}})
	if status != http.StatusOK {
		t.Fatalf("create = %d %v", status, body)
	}
	id := int64(body["id"].(float64))

	resp, _ := c.c.Get(srv.URL + "/api/notifiers")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), `"token_set":true`) {
		t.Fatalf("secret leaked or not flagged: %s", raw)
	}

	// Saving without the token keeps the stored one.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifiers/"+strconv.FormatInt(id, 10), strings.NewReader(
		`{"name":"phone 2","type":"telegram","enabled":true,"config":{"chat_id":"43"}}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.c.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("update failed: %v %v", err, resp.Status)
	}
	stored, _ := h.store.Notifier(id)
	var cfg notifierConfig
	json.Unmarshal([]byte(stored.Config), &cfg)
	if cfg.Token != "123:secret" || cfg.ChatID != "43" {
		t.Errorf("stored config after update: %+v", cfg)
	}
}
