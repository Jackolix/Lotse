package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/version"
)

const handshakeTimeout = 15 * time.Second

// maxFilesystems caps what a single sample may carry.
const maxFilesystems = 32

type agentConn struct {
	id       int64
	name     string
	conn     ssh.Conn
	remote   string
	info     protocol.SystemInfo
	features []string
}

func (ac *agentConn) has(feature string) bool {
	return slices.Contains(ac.features, feature)
}

// inNetwork reports whether the agent has an address inside n.
func (ac *agentConn) inNetwork(n *net.IPNet) bool {
	for _, iface := range ac.info.Interfaces {
		for _, addr := range iface.Addrs {
			if ip, _, err := net.ParseCIDR(addr); err == nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (ac *agentConn) setInterval(seconds int) {
	go protocol.Send(ac.conn, protocol.ReqInterval, false, protocol.IntervalMsg{Seconds: seconds}, 10*time.Second)
}

type statusEvent struct {
	ID     int64 `json:"id"`
	Online bool  `json:"online"`
}

// handleAgent upgrades an agent's request to a WebSocket and runs the SSH client
// side over it. Authentication happens inside SSH, not at the HTTP layer.
func (h *Hub) handleAgent(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already written the error response
	}
	h.agentWG.Add(1)
	defer h.agentWG.Done()
	conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
	defer conn.Close()

	remote := clientIP(r)
	if err := h.serveAgent(r.Context(), conn, remote); err != nil && r.Context().Err() == nil {
		h.log.Info("agent disconnected", "remote", remote, "err", err)
	}
}

func (h *Hub) serveAgent(ctx context.Context, conn net.Conn, remote string) error {
	var agentKey ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "hub",
		Auth: []ssh.AuthMethod{ssh.PublicKeys(h.signer)},
		// Any Ed25519 key may finish the handshake; admit() then requires the key to
		// belong to a known system or come with a valid enrollment token.
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			agentKey = key
			return nil
		},
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		ClientVersion:     "SSH-2.0-" + version.Name + "-hub",
	}
	timer := time.AfterFunc(handshakeTimeout, func() { conn.Close() })
	sc, chans, reqs, err := ssh.NewClientConn(conn, remote, cfg)
	timer.Stop()
	if err != nil {
		return fmt.Errorf("ssh handshake: %w", err)
	}
	defer sc.Close()
	go protocol.RejectChannels(chans)

	var req *ssh.Request
	select {
	case req = <-reqs:
	case <-time.After(handshakeTimeout):
		return errors.New("no hello from agent")
	}
	if req == nil {
		return errors.New("closed before hello")
	}
	if req.Type != protocol.ReqHello {
		req.Reply(false, nil)
		return fmt.Errorf("expected hello, got %q", req.Type)
	}
	var hello protocol.Hello
	if err := json.Unmarshal(req.Payload, &hello); err != nil {
		req.Reply(false, nil)
		return fmt.Errorf("bad hello: %w", err)
	}
	sys, err := h.admit(ssh.FingerprintSHA256(agentKey), &hello)
	if err != nil {
		protocol.Reply(req, false, protocol.HelloReply{Error: err.Error()})
		return fmt.Errorf("refused: %w", err)
	}

	ac := &agentConn{id: sys.ID, name: sys.Name, conn: sc, remote: remote, info: hello.Info, features: hello.Features}
	interval := h.register(ac)
	defer h.unregister(ac)
	protocol.Reply(req, true, protocol.HelloReply{OK: true, Interval: interval})
	h.log.Info("agent connected", "system", sys.Name, "id", sys.ID, "remote", remote, "agent_version", hello.AgentVersion, "features", hello.Features)

	watchdog := time.NewTicker(30 * time.Second)
	defer watchdog.Stop()
	lastMsg := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-watchdog.C:
			if time.Since(lastMsg) > agentTimeout {
				return errors.New("no data from agent, closing link")
			}
		case req, ok := <-reqs:
			if !ok {
				return errors.New("connection closed")
			}
			lastMsg = time.Now()
			if req.Type != protocol.ReqMetrics {
				req.Reply(false, nil)
				continue
			}
			var m protocol.Metrics
			if err := json.Unmarshal(req.Payload, &m); err != nil {
				req.Reply(false, nil)
				continue
			}
			if len(m.Filesystems) > maxFilesystems {
				m.Filesystems = m.Filesystems[:maxFilesystems]
			}
			h.record(sys.ID, &m)
			req.Reply(true, nil)
		}
	}
}

// admit maps an agent key to a system, enrolling it when it presents a valid token.
// Error messages are sent back to the agent, so they must not leak internals.
func (h *Hub) admit(fingerprint string, hello *protocol.Hello) (*store.System, error) {
	if hello.Protocol != protocol.Version {
		return nil, fmt.Errorf("agent speaks protocol %d, hub speaks %d; update the agent", hello.Protocol, protocol.Version)
	}
	infoJSON, _ := json.Marshal(hello.Info)
	info := string(infoJSON)

	sys, err := h.store.SystemByFingerprint(fingerprint)
	if err == nil {
		if sys.Info != info || sys.AgentVersion != hello.AgentVersion {
			if err := h.store.UpdateSystemInfo(sys.ID, info, hello.AgentVersion); err != nil {
				h.log.Error("updating system info failed", "system", sys.ID, "err", err)
			}
			sys.Info, sys.AgentVersion = info, hello.AgentVersion
			h.broker.publish("systems", nil)
		}
		return sys, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.log.Error("looking up agent failed", "err", err)
		return nil, errors.New("internal error")
	}

	if hello.Token == "" {
		return nil, errors.New("unknown agent; add it with an enrollment token from the hub UI")
	}
	ok, err := h.store.ValidEnrollToken(hashToken(hello.Token))
	if err != nil {
		h.log.Error("checking enrollment token failed", "err", err)
		return nil, errors.New("internal error")
	}
	if !ok {
		return nil, errors.New("enrollment token is invalid or expired")
	}
	name := strings.TrimSpace(hello.Info.Hostname)
	if name == "" {
		name = "unnamed"
	}
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	sys, err = h.store.CreateSystem(name, fingerprint, info, hello.AgentVersion)
	if err != nil {
		h.log.Error("creating system failed", "err", err)
		return nil, errors.New("internal error")
	}
	h.log.Info("enrolled new system", "system", sys.Name, "id", sys.ID, "fingerprint", fingerprint)
	h.audit(nil, "", "system_enrolled", sys, "agent key "+fingerprint)
	h.broker.publish("systems", nil)
	return sys, nil
}

// register records a connected agent and returns the report interval it should use.
// A second connection from the same system replaces the first.
func (h *Hub) register(ac *agentConn) int {
	h.mu.Lock()
	old := h.agents[ac.id]
	h.agents[ac.id] = ac
	interval := h.intervalLocked()
	h.mu.Unlock()
	if old != nil {
		old.conn.Close()
	}
	h.broker.publish("status", statusEvent{ID: ac.id, Online: true})
	h.agentOnline(ac.id)
	return interval
}

func (h *Hub) unregister(ac *agentConn) {
	h.mu.Lock()
	current := h.agents[ac.id] == ac
	if current {
		delete(h.agents, ac.id)
	}
	st := h.states[ac.id] // nil if the system was just deleted
	h.mu.Unlock()
	if !current {
		return
	}
	if st != nil {
		h.flushState(ac.id, st, true)
	}
	h.agentOffline(ac.id)
	h.broker.publish("status", statusEvent{ID: ac.id, Online: false})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
