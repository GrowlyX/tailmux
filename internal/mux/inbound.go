package mux

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"

	"tailscale.com/net/tsaddr"
)

// forwardInbound hands a tailnet TCP connection to this machine's own
// socket on the same port, like Tailscale does. It dials the LAN
// address, not loopback, so services bound to 0.0.0.0 or the LAN IP are
// reachable and 127.0.0.1-only ones stay private. tsnet listeners and
// serve config take the port first; this only sees what they don't.
//
// tsnet calls this under its own lock, so it must not block: the dial
// happens in the returned handler, after the peer's handshake is done.
// A port with nothing on it accepts and then closes instead of refusing.
func forwardInbound(_, dst netip.AddrPort) (func(net.Conn), bool) {
	return func(c net.Conn) {
		defer c.Close()
		ip, err := lanAddr()
		if err != nil {
			return
		}
		up, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(dst.Port()))), 5*time.Second)
		if err != nil {
			return
		}
		defer up.Close()
		pipe(c, c, up)
	}, true
}

// lanAddr is this machine's first LAN IPv4 address. Looked up per
// connection so a new DHCP lease or network doesn't need a restart.
func lanAddr() (netip.Addr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return netip.Addr{}, err
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := pfx.Addr()
			if ip.Is4() && !ip.IsLinkLocalUnicast() && !tsaddr.IsTailscaleIP(ip) {
				return ip, nil
			}
		}
	}
	return netip.Addr{}, errors.New("no LAN address")
}
