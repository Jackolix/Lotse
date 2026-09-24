package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/wol"
)

type wakeResult struct {
	Via       string   `json:"via"` // name of the relaying system, or "hub"
	Broadcast string   `json:"broadcast"`
	MACs      []string `json:"macs"`
}

func (h *Hub) postWake(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	var info protocol.SystemInfo
	_ = json.Unmarshal([]byte(sys.Info), &info)
	res, err := h.wake(sys.ID, info)
	if err != nil {
		h.audit(r, s.Username, "wake_failed", sys, err.Error())
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.audit(r, s.Username, "wake", sys, fmt.Sprintf("magic packet for %s sent via %s to %s",
		strings.Join(res.MACs, ", "), res.Via, res.Broadcast))
	writeJSON(w, http.StatusOK, res)
}

// wake sends magic packets for every known MAC of the target. An online agent in
// the target's subnet relays them, since broadcasts do not cross routers or leave a
// Docker bridge network. Without such an agent the hub tries itself, which only
// reaches the LAN when it runs with host networking.
func (h *Hub) wake(targetID int64, info protocol.SystemInfo) (*wakeResult, error) {
	type subnet struct {
		broadcast string
		network   *net.IPNet
	}
	var macs []string
	var subnets []subnet
	for _, iface := range info.Interfaces {
		if _, err := wol.ParseMAC(iface.MAC); err == nil && !slices.Contains(macs, iface.MAC) {
			macs = append(macs, iface.MAC)
		}
		for _, addr := range iface.Addrs {
			if bc, n, ok := wol.Broadcast(addr); ok {
				subnets = append(subnets, subnet{bc, n})
			}
		}
	}
	if len(macs) == 0 {
		return nil, errors.New("no MAC address is known for this system; it is recorded once an agent of version 0.2 or newer has connected")
	}

	type relay struct {
		ac   *agentConn
		name string // copied under the lock; renames update it
	}
	h.mu.Lock()
	var relays []relay
	for id, ac := range h.agents {
		if id != targetID && ac.has(protocol.FeatureWake) {
			relays = append(relays, relay{ac, ac.name})
		}
	}
	h.mu.Unlock()
	slices.SortFunc(relays, func(a, b relay) int { return int(a.ac.id - b.ac.id) })

	for _, sn := range subnets {
		for _, rl := range relays {
			if !rl.ac.inNetwork(sn.network) {
				continue
			}
			ok, _, err := protocol.Send(rl.ac.conn, protocol.ReqWake, true, protocol.WakeMsg{MACs: macs, Broadcast: sn.broadcast}, 5*time.Second)
			if err == nil && ok {
				return &wakeResult{Via: rl.name, Broadcast: sn.broadcast, MACs: macs}, nil
			}
			h.log.Warn("wake-on-lan relay failed", "relay", rl.name, "err", err)
		}
	}

	var targets, sent []string
	for _, sn := range subnets {
		targets = append(targets, sn.broadcast)
	}
	targets = append(targets, "255.255.255.255")
	slices.Sort(targets)
	for _, bc := range slices.Compact(targets) {
		if err := wol.Send(macs, bc); err != nil {
			h.log.Warn("sending wake-on-lan from the hub failed", "broadcast", bc, "err", err)
			continue
		}
		sent = append(sent, bc)
	}
	if len(sent) == 0 {
		return nil, errors.New("could not send the magic packet")
	}
	return &wakeResult{Via: "hub", Broadcast: strings.Join(sent, ", "), MACs: macs}, nil
}
