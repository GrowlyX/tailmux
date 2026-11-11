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
		up, err := dialLAN(dst.Port())
		if err != nil {
			return
		}
		defer up.Close()
		pipe(c, c, up)
	}, true
}

// dialLAN connects to port on this machine's LAN addresses, trying each
// in turn, so a service bound to only one interface (Wi-Fi but not
// Ethernet, say) is still found. A local dial to a closed port is
// refused at once, so walking the list is cheap.
func dialLAN(port uint16) (net.Conn, error) {
	ips, err := lanAddrs()
	if err != nil {
		return nil, err
	}
	p := strconv.Itoa(int(port))
	for _, ip := range ips {
		up, derr := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), p), 5*time.Second)
		if derr == nil {
			return up, nil
		}
		err = derr
	}
	return nil, err
}

// lanAddrs is this machine's LAN IPv4 addresses, in interface order.
// Looked up per connection so a new DHCP lease or network doesn't need
// a restart.
func lanAddrs() ([]netip.Addr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
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
			// Some VM bridges hold their subnet's network address
			// (192.168.x.0/24), which binds but can't be dialed.
			netAddr := pfx.Bits() < 31 && ip == pfx.Masked().Addr()
			if ip.Is4() && !netAddr && !ip.IsLinkLocalUnicast() && !tsaddr.IsTailscaleIP(ip) {
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("no LAN address")
	}
	return ips, nil
}
