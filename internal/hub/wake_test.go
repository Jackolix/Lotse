package hub

import (
	"encoding/json"
	"testing"

	"github.com/Jackolix/Lotse/internal/protocol"
)

func TestWakeUsesAgentInSameSubnet(t *testing.T) {
	h, srv := newTestHub(t)
	relay := enroll(t, h, srv, false)

	h.mu.Lock()
	relayInfo := h.agents[relay.ID].info
	h.mu.Unlock()
	if len(relayInfo.Interfaces) == 0 {
		t.Skip("test machine has no physical network interface")
	}

	// A sleeping machine in the relay's subnet.
	target := protocol.SystemInfo{Interfaces: []protocol.NetInterface{
		{Name: "eth0", MAC: "02:00:00:00:00:99", Addrs: relayInfo.Interfaces[0].Addrs},
	}}
	infoJSON, _ := json.Marshal(target)
	sys, err := h.store.CreateSystem("sleepy", "SHA256:sleepy", string(infoJSON), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.wake(sys.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if res.Via != relay.Name {
		t.Errorf("sent via %q, want the relay %q", res.Via, relay.Name)
	}

	// A machine in a network no agent is part of falls back to the hub.
	elsewhere := protocol.SystemInfo{Interfaces: []protocol.NetInterface{
		{Name: "eth0", MAC: "02:00:00:00:00:98", Addrs: []string{"10.250.0.5/24"}},
	}}
	res, err = h.wake(sys.ID, elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if res.Via != "hub" {
		t.Errorf("sent via %q, want hub", res.Via)
	}

	if _, err := h.wake(sys.ID, protocol.SystemInfo{}); err == nil {
		t.Error("waking a system without known MAC should fail")
	}
}
