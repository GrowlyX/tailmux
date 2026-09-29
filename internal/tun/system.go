package tun

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	wgtun "github.com/tailscale/wireguard-go/tun"

	"github.com/GrowlyX/tailmux/internal/mux"
)

// osConfig is the per-OS part: addressing the interface, routes, and
// pointing the resolver at tailmux for tailnet domains only.
type osConfig interface {
	up(ifname string, gw netip.Addr, fakeRange netip.Prefix, mtu int) error
	addRoute(ifname string, p netip.Prefix) error
	delRoute(ifname string, p netip.Prefix) error
	// setDNS routes queries for domains (and their subdomains) to server.
	// It reports false if the OS offers no way to do that.
	setDNS(ifname string, domains []string, server netip.Addr) (bool, error)
	close(ifname string) error
}

// System is a running TUN: device, engine and OS configuration, kept in
// sync with the routing table.
type System struct {
	m      *mux.Mux
	cfg    *mux.Config
	os     osConfig
	eng    *Engine
	fake   *FakeIPs
	ifname string

	mu        sync.Mutex
	routes    map[netip.Prefix]bool
	domains   []string
	dnsActive bool
	skipped   map[netip.Prefix]bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// Start creates the TUN device and starts routing tailnet traffic
// through it. It needs root.
func Start(ctx context.Context, m *mux.Mux, cfg *mux.Config) (*System, error) {
	osc, err := newOSConfig()
	if err != nil {
		return nil, err
	}
	name := cfg.TUN.Name
	if name == "" {
		name = defaultTUNName
	}
	dev, err := wgtun.CreateTUN(name, cfg.TUN.MTU)
	if err != nil {
		return nil, fmt.Errorf("create tun %q (are you root?): %w", name, err)
	}
	ifname, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, err
	}
	fake := NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), filepath.Join(cfg.StateDir, "fakeip.json"))
	eng, err := NewEngine(m, dev, fake, cfg.TUN.MTU)
	if err != nil {
		dev.Close()
		return nil, err
	}
	if err := osc.up(ifname, fake.Gateway(), fake.Prefix(), cfg.TUN.MTU); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure %s: %w", ifname, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &System{m: m, cfg: cfg, os: osc, eng: eng, fake: fake, ifname: ifname,
		routes: map[netip.Prefix]bool{}, skipped: map[netip.Prefix]bool{}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		if err := eng.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("tun: %v", err)
		}
	}()
	m.OnChange(s.reconcile)
	s.reconcile()
	log.Printf("tun      %s  (fake IPs %s, DNS %s)", ifname, fake.Prefix(), fake.DNS())
	return s, nil
}

func (s *System) Interface() string { return s.ifname }
func (s *System) FakeIPs() *FakeIPs { return s.fake }

// DNSActive reports whether the OS resolver sends tailnet domains here.
func (s *System) DNSActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dnsActive
}

// reconcile makes the OS routes and DNS domains match what the running
// tailnets claim.
func (s *System) reconcile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.m.Router()
	prefixes, domains := r.Claims()
	// Devices' own addresses: a name in public DNS pointing at a tailnet
	// device (grafana.example.com -> 100.x) has to land here too. A /32
	// beats the official client's 100.64.0.0/10, so both can coexist.
	if s.cfg.TUN.PeerRoutes == nil || *s.cfg.TUN.PeerRoutes {
		prefixes = append(prefixes, r.PeerAddrs()...)
	}
	if s.cfg.TUN.CGNAT {
		prefixes = append(prefixes, netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48"))
	}
	pinned := map[netip.Prefix]bool{}
	for k := range s.cfg.Pins {
		if p, err := netip.ParsePrefix(k); err == nil {
			pinned[p.Masked()] = true
		}
	}
	local := localPrefixes(s.ifname)
	want := map[netip.Prefix]bool{}
	for _, p := range prefixes {
		if !pinned[p] && overlapsAny(p, local) {
			// Routing your own LAN into the tunnel would cut you off from
			// it. Pin the prefix to force it.
			if !s.skipped[p] {
				log.Printf("tun: not routing %s: overlaps a local network (pin it to override)", p)
				s.skipped[p] = true
			}
			continue
		}
		want[p] = true
	}
	for p := range s.routes {
		if !want[p] {
			if err := s.os.delRoute(s.ifname, p); err != nil {
				log.Printf("tun: remove route %s: %v", p, err)
			}
			delete(s.routes, p)
		}
	}
	for p := range want {
		if !s.routes[p] {
			if err := s.os.addRoute(s.ifname, p); err != nil {
				log.Printf("tun: add route %s: %v", p, err)
				continue
			}
			s.routes[p] = true
		}
	}
	if s.cfg.TUN.NoDNS || slices.Equal(domains, s.domains) {
		return
	}
	ok, err := s.os.setDNS(s.ifname, domains, s.fake.DNS())
	if err != nil {
		log.Printf("tun: dns: %v", err)
		return
	}
	s.domains, s.dnsActive = domains, ok
	if ok && len(domains) > 0 {
		log.Printf("tun: resolving %s via %s", strings.Join(domains, ", "), s.fake.DNS())
	}
}

func (s *System) Close() error {
	s.cancel()
	<-s.done
	return s.os.close(s.ifname)
}

func localPrefixes(skip string) []netip.Prefix {
	ifs, _ := net.Interfaces()
	var out []netip.Prefix
	for _, ifc := range ifs {
		if ifc.Name == skip || ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				ip, _ := netip.AddrFromSlice(ipn.IP)
				ones, _ := ipn.Mask.Size()
				ip = ip.Unmap()
				if ip.IsLinkLocalUnicast() {
					continue
				}
				out = append(out, netip.PrefixFrom(ip, ones).Masked())
			}
		}
	}
	return out
}

func overlapsAny(p netip.Prefix, list []netip.Prefix) bool {
	for _, l := range list {
		if p.Overlaps(l) {
			return true
		}
	}
	return false
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }
