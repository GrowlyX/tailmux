//go:build darwin || windows

package tun

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
)

// exitRoutes cover everything, but as halves: they beat the default
// route without replacing it, so the OS (and tsnet, which binds its
// sockets to the default route's interface) still knows the real one,
// and the LAN's more specific routes still win.
var exitRoutes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"),
	netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1"),
}

// setExitRoutes adds or removes exitRoutes with add/del. Only IPv4 errors
// count: a TUN without IPv6 can't take the IPv6 halves.
func setExitRoutes(on bool, add, del func(netip.Prefix) error) error {
	var errs []error
	for _, p := range exitRoutes {
		f := del
		if on {
			f = add
		}
		if err := f(p); err != nil && on {
			if p.Addr().Is4() {
				errs = append(errs, fmt.Errorf("%s: %w", p, err))
			} else {
				log.Printf("tun: exit node: no IPv6 route %s: %v", p, err)
			}
		}
	}
	return errors.Join(errs...)
}
