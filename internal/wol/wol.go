// Package wol sends Wake-on-LAN magic packets.
package wol

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Port is the conventional "discard" port; NICs match the payload, not the port.
const Port = 9

// MagicPacket is 6×0xFF followed by the MAC address 16 times.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, 0, 102)
	for range 6 {
		pkt = append(pkt, 0xff)
	}
	for range 16 {
		pkt = append(pkt, hw...)
	}
	return pkt, nil
}

// ParseMAC accepts a 48-bit unicast MAC address.
func ParseMAC(mac string) (net.HardwareAddr, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	if len(hw) != 6 || hw[0]&1 == 1 || hw.String() == "00:00:00:00:00:00" {
		return nil, fmt.Errorf("%s is not a unicast 48-bit MAC address", mac)
	}
	return hw, nil
}

// Send broadcasts magic packets for every MAC to broadcast:9, three times each
// because UDP broadcasts are occasionally dropped.
func Send(macs []string, broadcast string) error {
	ip := net.ParseIP(broadcast).To4()
	if ip == nil {
		return fmt.Errorf("invalid IPv4 broadcast address %q", broadcast)
	}
	if len(macs) == 0 {
		return errors.New("no MAC address to wake")
	}
	packets := make([][]byte, 0, len(macs))
	for _, mac := range macs {
		pkt, err := MagicPacket(mac)
		if err != nil {
			return err
		}
		packets = append(packets, pkt)
	}
	// Go enables SO_BROADCAST on UDP sockets by default.
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: Port})
	if err != nil {
		return err
	}
	defer conn.Close()
	for round := range 3 {
		if round > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		for _, pkt := range packets {
			if _, err := conn.Write(pkt); err != nil {
				return err
			}
		}
	}
	return nil
}

// Broadcast returns the IPv4 broadcast address of a network given in CIDR form
// ("192.168.1.20/24" → "192.168.1.255"). ok is false for IPv6 or /31, /32 networks.
func Broadcast(cidr string) (addr string, network *net.IPNet, ok bool) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", nil, false
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", nil, false
	}
	b := make(net.IP, 4)
	for i := range 4 {
		b[i] = ipnet.IP.To4()[i] | ^ipnet.Mask[i]
	}
	return b.String(), ipnet, true
}

// IsLocalBroadcast reports whether addr is the limited broadcast address or the
// broadcast address of a network this machine is attached to. Relays only send
// there, so a hub cannot use them to spray packets at arbitrary hosts.
func IsLocalBroadcast(addr string) bool {
	if addr == "255.255.255.255" {
		return true
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range ifaces {
		if bc, _, ok := Broadcast(a.String()); ok && bc == addr {
			return true
		}
	}
	return false
}
