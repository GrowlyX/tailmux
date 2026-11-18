package mux

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalDNSForLocalNames(t *testing.T) {
	var asked atomic.Bool
	offline := netip.MustParseAddrPort("127.0.0.1:9") // nothing listens
	m := New(&Config{}, Options{})
	m.SetLocalDNS(func() []netip.AddrPort {
		asked.Store(true)
		return []netip.AddrPort{offline}
	})
	for host, want := range map[string]bool{"printer.home.arpa": true, "printer.local": false} {
		asked.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		m.Resolve(ctx, host)
		cancel()
		if asked.Load() != want {
			t.Errorf("%s: local nameservers asked = %v, want %v", host, !want, want)
		}
	}
}
