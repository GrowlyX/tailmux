package mux

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/net/netns"

	"github.com/GrowlyX/tailmux/internal/lab"
)

func TestSystemResolverForLocalNames(t *testing.T) {
	var used atomic.Bool
	m := New(&Config{}, Options{})
	m.SetSystemResolver(&net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		used.Store(true)
		return nil, errors.New("offline")
	}})
	for host, want := range map[string]bool{"printer.home.arpa": true, "printer.local": false} {
		used.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		m.Resolve(ctx, host)
		cancel()
		if used.Load() != want {
			t.Errorf("%s: replacement resolver used = %v, want %v", host, !want, want)
		}
	}
}

func TestOverrideLocalDNS(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up a tailnet")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	answer := netip.MustParseAddr("203.0.113.53")
	f := lab.NewTailnet(t, "override")
	f.GlobalDNS(t, ctx, "corp.internal", answer)
	cfg := &Config{StateDir: t.TempDir(), Hostname: "tailmux", Tailnets: []TailnetConfig{{Name: "override", ControlURL: f.URL, Ephemeral: true}}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}

	lab.Eventually(t, "the tailnet's nameserver answers every name", 90*time.Second, func() error {
		tgt, err := m.Resolve(ctx, "db.corp.internal")
		if err != nil || !m.TakesAllDNS() || !slices.Equal(tgt.IPs, []netip.Addr{answer}) {
			return errors.Join(err, errors.New("not yet"))
		}
		return nil
	})
}
