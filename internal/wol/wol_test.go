package wol

import (
	"bytes"
	"testing"
)

func TestMagicPacket(t *testing.T) {
	pkt, err := MagicPacket("AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) != 102 || !bytes.Equal(pkt[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatalf("bad header: % x", pkt[:6])
	}
	mac := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	if !bytes.Equal(pkt[6:], bytes.Repeat(mac, 16)) {
		t.Fatal("MAC not repeated 16 times")
	}
	for _, bad := range []string{"ff:ff:ff:ff:ff:ff", "01:00:5e:00:00:01", "00:00:00:00:00:00", "nope"} {
		if _, err := MagicPacket(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestBroadcast(t *testing.T) {
	cases := map[string]string{
		"192.168.1.20/24": "192.168.1.255",
		"10.1.2.3/8":      "10.255.255.255",
		"172.16.5.9/20":   "172.16.15.255",
	}
	for cidr, want := range cases {
		if got, _, ok := Broadcast(cidr); !ok || got != want {
			t.Errorf("Broadcast(%s) = %s, %v; want %s", cidr, got, ok, want)
		}
	}
	for _, cidr := range []string{"fe80::1/64", "10.0.0.1/32", "garbage"} {
		if _, _, ok := Broadcast(cidr); ok {
			t.Errorf("Broadcast(%s) should not be ok", cidr)
		}
	}
}
