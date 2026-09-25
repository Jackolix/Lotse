// Package agent connects a host to the hub and reports its metrics.
package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/agent/collect"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/sshkey"
	"github.com/Jackolix/Lotse/internal/version"
	"github.com/Jackolix/Lotse/internal/wol"
)

const (
	minBackoff     = time.Second
	maxBackoff     = time.Minute
	requestTimeout = 15 * time.Second
)

type Agent struct {
	cfg       *Config
	hubKey    ssh.PublicKey
	signer    ssh.Signer
	collector *collect.Collector
	info      protocol.SystemInfo
	log       *slog.Logger
	sessions  atomic.Int32 // open session channels: shells, file transfers, scripts
	updating  atomic.Bool

	// Executable is the installed agent binary that updates replace; empty means
	// os.Executable().
	Executable string
	// Restart starts the new binary after an update. cmd/agent sets it; without it
	// the old version keeps running until the service restarts.
	Restart func(exe string)
}

func New(cfg *Config, log *slog.Logger) (*Agent, error) {
	hubKey, err := sshkey.ParseAuthorizedKey(cfg.HubKey)
	if err != nil {
		return nil, fmt.Errorf("hub key: %w", err)
	}
	signer, err := sshkey.LoadOrCreate(cfg.KeyPath())
	if err != nil {
		return nil, fmt.Errorf("agent key: %w", err)
	}
	return &Agent{
		cfg:       cfg,
		hubKey:    hubKey,
		signer:    signer,
		collector: collect.New(),
		info:      collect.Info(),
		log:       log,
	}, nil
}

// Fingerprint identifies this agent on the hub.
func (a *Agent) Fingerprint() string {
	return ssh.FingerprintSHA256(a.signer.PublicKey())
}

// Run keeps a connection to the hub open until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	a.log.Info("agent starting", "version", version.Version, "hub", a.cfg.HubURL, "fingerprint", a.Fingerprint())
	a.cleanupUpdate()
	backoff := minBackoff
	var lastErr string
	var lastLogged time.Time
	for {
		start := time.Now()
		connected, err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			lastErr = ""
			if time.Since(start) > time.Minute {
				backoff = minBackoff
			}
		}
		// While the hub is unreachable, log a repeated error only every 10 minutes.
		if msg := err.Error(); msg != lastErr || time.Since(lastLogged) > 10*time.Minute {
			a.log.Warn("not connected to hub", "err", err, "retry_in", backoff.Round(time.Second))
			lastErr, lastLogged = msg, time.Now()
		}
		wait := backoff + rand.N(backoff/2+1)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session runs one connection until it fails. connected reports whether the hub accepted us.
func (a *Agent) session(ctx context.Context) (connected bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(ctx, requestTimeout)
	ws, _, err := websocket.Dial(dialCtx, a.wsURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": {version.AgentName + "/" + version.Version}},
	})
	dialCancel()
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()

	sconn, chans, reqs, err := a.handshake(conn)
	if err != nil {
		return false, err
	}
	defer sconn.Close()
	go a.handleChannels(chans)
	go func() {
		sconn.Wait()
		cancel()
	}()

	intervals := make(chan time.Duration, 1)
	go a.handleRequests(reqs, intervals)

	interval, err := a.hello(sconn)
	if err != nil {
		return false, err
	}
	a.log.Info("connected to hub", "interval", interval)
	return true, a.report(ctx, sconn, interval, intervals)
}

// handshake runs the SSH server side. Only the pinned hub key may authenticate.
func (a *Agent) handshake(conn net.Conn) (*ssh.ServerConn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare(key.Marshal(), a.hubKey.Marshal()) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("unexpected hub key")
		},
		ServerVersion: "SSH-2.0-" + version.AgentName,
	}
	cfg.AddHostKey(a.signer)

	timer := time.AfterFunc(requestTimeout, func() { conn.Close() })
	defer timer.Stop()
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh handshake failed (does the hub key in the config match the hub?): %w", err)
	}
	return sconn, chans, reqs, nil
}

func (a *Agent) hello(conn ssh.Conn) (time.Duration, error) {
	msg := protocol.Hello{Protocol: protocol.Version, AgentVersion: version.Version, Token: a.cfg.Token, Info: a.info, Features: a.features()}
	ok, payload, err := protocol.Send(conn, protocol.ReqHello, true, msg, requestTimeout)
	if err != nil {
		return 0, fmt.Errorf("hello: %w", err)
	}
	var reply protocol.HelloReply
	_ = json.Unmarshal(payload, &reply)
	if !ok || !reply.OK {
		if reply.Error == "" {
			reply.Error = "rejected"
		}
		return 0, fmt.Errorf("hub refused this agent: %s", reply.Error)
	}
	if a.cfg.Token != "" {
		// Enrolled: the hub now knows our key. Dropping the token also means a system
		// deleted in the UI does not silently re-enroll while the token is still valid.
		a.cfg.Token = ""
		if err := a.cfg.Save(); err != nil {
			a.log.Warn("could not remove enrollment token from config", "err", err)
		}
	}
	return clampInterval(reply.Interval), nil
}

func (a *Agent) report(ctx context.Context, conn ssh.Conn, interval time.Duration, intervals <-chan time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		m := a.collector.Sample()
		ok, _, err := protocol.Send(conn, protocol.ReqMetrics, true, m, requestTimeout)
		if err != nil {
			return fmt.Errorf("send metrics: %w", err)
		}
		if !ok {
			return errors.New("hub rejected metrics")
		}
		select {
		case <-ctx.Done():
			return errors.New("connection closed")
		case <-ticker.C:
		case d := <-intervals:
			// Report right away so a newly opened dashboard fills quickly.
			if d != interval {
				interval = d
				ticker.Reset(d)
			}
		}
	}
}

// features tells the hub what this agent may do. The shell is opt-in per machine.
func (a *Agent) features() []string {
	f := []string{protocol.FeatureWake}
	if a.cfg.AllowShell {
		f = append(f, protocol.FeatureShell)
	}
	if !a.cfg.DisableUpdates {
		f = append(f, protocol.FeatureUpdate)
	}
	return f
}

const remoteControlOff = "is disabled on this machine (install the agent with --allow-shell)"

func (a *Agent) handleRequests(reqs <-chan *ssh.Request, intervals chan time.Duration) {
	for req := range reqs {
		switch req.Type {
		case protocol.ReqProcesses:
			var q protocol.ProcessQuery
			_ = json.Unmarshal(req.Payload, &q)
			// Sampling takes half a second; don't hold up other requests meanwhile.
			go func() {
				list, err := collect.Processes(min(max(q.Limit, 1), 100), a.cfg.AllowShell)
				if err != nil {
					a.log.Warn("listing processes failed", "err", err)
					req.Reply(false, nil)
					return
				}
				protocol.Reply(req, true, list)
			}()
		case protocol.ReqSignal:
			var msg protocol.SignalMsg
			if err := json.Unmarshal(req.Payload, &msg); err != nil {
				req.Reply(false, nil)
				continue
			}
			var reply protocol.ActionReply
			if !a.cfg.AllowShell {
				reply.Error = "stopping processes " + remoteControlOff
			} else if err := collect.Signal(msg.PID, msg.Signal); err != nil {
				reply.Error = err.Error()
			} else {
				a.log.Info("process signaled by hub", "pid", msg.PID, "signal", msg.Signal)
			}
			protocol.Reply(req, reply.Error == "", reply)
		case protocol.ReqPower:
			var msg protocol.PowerMsg
			if err := json.Unmarshal(req.Payload, &msg); err != nil {
				req.Reply(false, nil)
				continue
			}
			var reply protocol.ActionReply
			var cmd *exec.Cmd
			if !a.cfg.AllowShell {
				reply.Error = "rebooting and shutting down " + remoteControlOff
			} else if c, err := powerCommand(msg.Action); err != nil {
				reply.Error = err.Error()
			} else {
				cmd = c
			}
			protocol.Reply(req, reply.Error == "", reply)
			if cmd != nil {
				a.log.Warn("power action requested by hub", "action", msg.Action, "command", cmd.String())
				time.AfterFunc(time.Second, func() { // let the reply reach the hub first
					if out, err := cmd.CombinedOutput(); err != nil {
						a.log.Error("power action failed", "action", msg.Action, "err", err, "output", strings.TrimSpace(string(out)))
					}
				})
			}
		case protocol.ReqServices:
			go protocol.Reply(req, true, listServices())
		case protocol.ReqService:
			var msg protocol.ServiceMsg
			if err := json.Unmarshal(req.Payload, &msg); err != nil {
				req.Reply(false, nil)
				continue
			}
			go func() {
				var reply protocol.ActionReply
				if !a.cfg.AllowShell {
					reply.Error = "controlling services " + remoteControlOff
				} else if err := controlService(msg.Name, msg.Action); err != nil {
					reply.Error = err.Error()
				} else {
					a.log.Info("service controlled by hub", "service", msg.Name, "action", msg.Action)
				}
				protocol.Reply(req, reply.Error == "", reply)
			}()
		case protocol.ReqWake:
			var msg protocol.WakeMsg
			if err := json.Unmarshal(req.Payload, &msg); err != nil || !wol.IsLocalBroadcast(msg.Broadcast) {
				req.Reply(false, nil)
				continue
			}
			err := wol.Send(msg.MACs, msg.Broadcast)
			if err != nil {
				a.log.Warn("sending wake-on-lan packet failed", "err", err)
			} else {
				a.log.Info("sent wake-on-lan packet", "macs", msg.MACs, "broadcast", msg.Broadcast)
			}
			req.Reply(err == nil, nil)
		case protocol.ReqInterval:
			var msg protocol.IntervalMsg
			if err := json.Unmarshal(req.Payload, &msg); err != nil || msg.Seconds <= 0 {
				req.Reply(false, nil)
				continue
			}
			select { // keep only the newest value; this goroutine is the only sender
			case <-intervals:
			default:
			}
			intervals <- clampInterval(msg.Seconds)
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

func clampInterval(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(min(max(seconds, 1), 3600)) * time.Second
}

func (a *Agent) wsURL() string {
	u, _ := url.Parse(a.cfg.HubURL) // validated in Config.Validate
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + protocol.AgentPath
	return u.String()
}
