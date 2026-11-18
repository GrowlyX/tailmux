package mux

import (
	"context"
	"errors"
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
	// Unknown nameservers: the OS would send the name back here.
	m.SetLocalDNS(func() []netip.AddrPort { return nil })
	if _, err := m.Resolve(context.Background(), "printer.home.arpa"); !errors.Is(err, ErrNoLocalDNS) {
		t.Errorf("no local nameservers: %v, want %v", err, ErrNoLocalDNS)
	}
}
