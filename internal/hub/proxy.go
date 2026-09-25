package hub

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// parseProxies reads HUB_TRUSTED_PROXIES: IP addresses and CIDR ranges separated by commas.
func parseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", f)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func (h *Hub) trustedProxy(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range h.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// realIP puts the client's address in RemoteAddr when a request comes through a
// trusted reverse proxy (Caddy, Traefik, cloudflared, ...). Without it every client
// shares the proxy's address: one stranger's failed logins lock everyone out, and
// the activity log shows only the proxy.
func (h *Hub) realIP(next http.Handler) http.Handler {
	if len(h.proxies) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := h.forwardedFor(r); ok {
			r.RemoteAddr = netip.AddrPortFrom(ip, 0).String()
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedFor reads X-Forwarded-For from the right. Each proxy appends the address
// it got the request from, so the first address that is not a trusted proxy is the
// client; anything left of it may be forged by the client itself.
func (h *Hub) forwardedFor(r *http.Request) (netip.Addr, bool) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !h.trustedProxy(peer.Addr()) {
		return netip.Addr{}, false
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return netip.Addr{}, false
		}
		if !h.trustedProxy(a) {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}
