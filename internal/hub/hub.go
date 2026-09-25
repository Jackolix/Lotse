// Package hub is the central server: it accepts agent connections, stores metrics
// and serves the web UI and its API.
package hub

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/sshkey"
	"github.com/Jackolix/Lotse/internal/update"
	"github.com/Jackolix/Lotse/internal/version"
)

const (
	liveInterval = 2  // seconds between agent reports while someone has the UI open
	idleInterval = 60 // seconds between reports otherwise
	idleDelay    = 15 * time.Second
	agentTimeout = 150 * time.Second // no message for this long means the link is dead
	liveRingSize = 180               // recent samples kept per system for the "Live" chart
	enrollTTL    = time.Hour
	sessionTTL   = 30 * 24 * time.Hour
)

type Config struct {
	Addr      string // HUB_ADDR, listen address
	DataDir   string // HUB_DATA_DIR, SQLite database and hub key
	PublicURL string // HUB_URL, how agents reach the hub; derived from the browser's URL when empty
	AgentDir  string // HUB_AGENT_DIR, prebuilt agent binaries served at /download
	// TrustedProxies (HUB_TRUSTED_PROXIES) lists the reverse proxies, as IPs or CIDRs
	// separated by commas, whose X-Forwarded-For header names the real client.
	TrustedProxies string
}

func ConfigFromEnv() Config {
	env := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	return Config{
		Addr:      env("HUB_ADDR", ":8090"),
		DataDir:   env("HUB_DATA_DIR", "./data"),
		PublicURL: strings.TrimSuffix(os.Getenv("HUB_URL"), "/"),
		AgentDir:  env("HUB_AGENT_DIR", "./dist/agents"),

		TrustedProxies: os.Getenv("HUB_TRUSTED_PROXIES"),
	}
}

type Hub struct {
	cfg     Config
	log     *slog.Logger
	store   *store.Store
	signer  ssh.Signer
	broker  *broker
	limiter *loginLimiter
	proxies []netip.Prefix // trusted reverse proxies
	setupMu sync.Mutex
	agentWG sync.WaitGroup

	alerts     *alertEngine
	totpMu     sync.Mutex
	totpUsed   map[int64]int64             // last accepted TOTP time step per user, against replays
	challenges challenges                  // pending passkey ceremonies
	updates    map[string]*update.Manifest // signed agent binaries by file name; read-only after New

	// ctx ends when the hub shuts down; script runs, which outlive their request, use it.
	ctx   context.Context
	stop  context.CancelFunc
	runWG sync.WaitGroup

	mu        sync.Mutex
	agents    map[int64]*agentConn // connected agents by system ID
	states    map[int64]*sysState
	shells    map[*shellSession]struct{}
	runs      map[int64]*activeRun // script runs in progress
	live      bool                 // agents currently report at liveInterval
	idleTimer *time.Timer
}

func New(cfg Config, log *slog.Logger) (*Hub, error) {
	proxies, err := parseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("HUB_TRUSTED_PROXIES: %w", err)
	}
	st, signer, err := openData(cfg)
	if err != nil {
		return nil, err
	}
	h := &Hub{
		cfg:      cfg,
		log:      log,
		store:    st,
		signer:   signer,
		limiter:  newLoginLimiter(),
		proxies:  proxies,
		totpUsed: map[int64]int64{},
		agents:   map[int64]*agentConn{},
		states:   map[int64]*sysState{},
		shells:   map[*shellSession]struct{}{},
		runs:     map[int64]*activeRun{},
		alerts:   &alertEngine{states: map[alertKey]*alertState{}, offline: map[int64]time.Time{}, started: time.Now()},
	}
	h.ctx, h.stop = context.WithCancel(context.Background())
	h.broker = newBroker(h.viewersChanged)
	if err := h.loadAlerts(); err != nil {
		st.Close()
		return nil, fmt.Errorf("loading alerts: %w", err)
	}
	if err := h.finishInterruptedRuns(); err != nil {
		st.Close()
		return nil, fmt.Errorf("closing interrupted script runs: %w", err)
	}
	h.loadUpdates()
	return h, nil
}

// stopRuns cancels script runs and waits for their results to be stored.
func (h *Hub) stopRuns(timeout time.Duration) {
	h.stop()
	done := make(chan struct{})
	go func() {
		h.runWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func openData(cfg Config) (*store.Store, ssh.Signer, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, permissionHint(cfg.DataDir, err)
	}
	signer, err := sshkey.LoadOrCreate(filepath.Join(cfg.DataDir, "hub_ed25519"))
	if err != nil {
		return nil, nil, permissionHint(cfg.DataDir, fmt.Errorf("hub key: %w", err))
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("database: %w", err)
	}
	return st, signer, nil
}

// permissionHint explains the usual cause of an unwritable data directory: a bind
// mount owned by another user while the container runs as a fixed non-root user.
func permissionHint(dir string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w\n\nThe hub runs as user %d and cannot write to %s. Either start the container as root "+
		"(the image default), so it can take over the folder itself, or run on the host:\n  chown -R %d:%d <the folder mounted at %s>",
		err, os.Getuid(), dir, os.Getuid(), os.Getgid(), dir)
}

// PublicKey is the hub identity that agents pin, in authorized_keys format.
func (h *Hub) PublicKey() string {
	return sshkey.AuthorizedKey(h.signer.PublicKey())
}

// Run serves HTTP until ctx is cancelled, then shuts down cleanly.
func (h *Hub) Run(ctx context.Context) error {
	go h.maintenance(ctx)

	srv := &http.Server{
		Addr:              h.cfg.Addr,
		Handler:           h.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Cancelling ctx ends long-lived requests (event streams, agent links) on shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	h.log.Info("hub listening", "addr", h.cfg.Addr, "version", version.Version, "hub_key", h.PublicKey())

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	h.stopRuns(5 * time.Second)
	h.waitAgents(5 * time.Second)
	h.flush(true)
	return h.store.Close()
}

// waitAgents waits for agent handlers (hijacked connections Shutdown does not track).
func (h *Hub) waitAgents(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		h.agentWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (h *Hub) maintenance(ctx context.Context) {
	h.rollup(store.Retention[store.Res1]) // catch up after downtime
	h.prune()
	flush := time.NewTicker(10 * time.Second)
	offline := time.NewTicker(15 * time.Second)
	rollup := time.NewTicker(5 * time.Minute)
	prune := time.NewTicker(time.Hour)
	defer flush.Stop()
	defer offline.Stop()
	defer rollup.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			h.flush(false)
		case now := <-offline.C:
			h.evaluateOffline(now)
		case <-rollup.C:
			h.rollup(2 * time.Hour)
		case <-prune.C:
			h.prune()
		}
	}
}

func (h *Hub) rollup(window time.Duration) {
	now := time.Now()
	for _, res := range []int{store.Res10, store.Res60} {
		if err := h.store.Rollup(res, window, now); err != nil {
			h.log.Error("rollup failed", "res", res, "err", err)
		}
	}
}

func (h *Hub) prune() {
	if err := h.store.PruneMetrics(time.Now()); err != nil {
		h.log.Error("pruning metrics failed", "err", err)
	}
	if err := h.store.DeleteExpired(); err != nil {
		h.log.Error("deleting expired sessions failed", "err", err)
	}
	if err := h.store.PruneAudit(time.Now()); err != nil {
		h.log.Error("pruning audit log failed", "err", err)
	}
	if err := h.store.PruneAlerts(time.Now()); err != nil {
		h.log.Error("pruning alert history failed", "err", err)
	}
	if err := h.store.PruneRuns(time.Now()); err != nil {
		h.log.Error("pruning script runs failed", "err", err)
	}
}

// viewersChanged switches agents between live and idle reporting. Going idle waits
// a little, so a page reload does not flap every agent.
func (h *Hub) viewersChanged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.broker.count() > 0 {
		if h.idleTimer != nil {
			h.idleTimer.Stop()
			h.idleTimer = nil
		}
		if !h.live {
			h.live = true
			h.broadcastIntervalLocked()
		}
		return
	}
	if h.live && h.idleTimer == nil {
		h.idleTimer = time.AfterFunc(idleDelay, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.idleTimer = nil
			if h.live && h.broker.count() == 0 {
				h.live = false
				h.broadcastIntervalLocked()
			}
		})
	}
}

func (h *Hub) intervalLocked() int {
	if h.live {
		return liveInterval
	}
	return idleInterval
}

func (h *Hub) broadcastIntervalLocked() {
	sec := h.intervalLocked()
	for _, ac := range h.agents {
		ac.setInterval(sec)
	}
}

// CreateEnrollToken creates a token directly in the database, for the "hub token"
// command. It is safe to run while the server is up (SQLite WAL handles both).
func CreateEnrollToken(cfg Config, ttl time.Duration) (token string, expires time.Time, hubKey string, err error) {
	st, signer, err := openData(cfg)
	if err != nil {
		return "", time.Time{}, "", err
	}
	defer st.Close()
	token = randomToken()
	expires = time.Now().Add(ttl)
	if err := st.CreateEnrollToken(hashToken(token), expires); err != nil {
		return "", time.Time{}, "", err
	}
	return token, expires, sshkey.AuthorizedKey(signer.PublicKey()), nil
}
