package tun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// searchDomains supplies ordered suffixes for bare names where supported.
	// It reports false if the OS offers no way to do that.
	setDNS(ifname string, domains, searchDomains []string, server netip.Addr) (bool, error)
	// dnsIntact reports whether the OS still has what setDNS configured.
	dnsIntact(ifname string, domains, searchDomains []string) bool
	// flushDNS drops cached answers (including cached failures).
	flushDNS()
	// setExit sends (or stops sending) everything into the TUN, for an
	// exit node. tsnet's own traffic must keep using the real network.
	setExit(ifname string, on bool) error
	close(ifname string) error
}

// System is a running TUN: device, engine and OS configuration, kept in
// sync with the routing table. If the device dies, System creates a new
// one (the name may change: utun5 -> utun6) and re-applies everything.
type System struct {
	m    *mux.Mux
	cfg  *mux.Config
	os   osConfig
	fake *FakeIPs

	// mu keeps OS configuration and device closure in the same lifetime.
	mu     sync.Mutex
	dev    wgtun.Device
	ifname string

	closeErr      error
	routes        map[netip.Prefix]bool
	routedNow     atomic.Pointer[[]netip.Prefix] // routes, for DNS answers without s.mu
	domains       []string
	searchDomains []string
	dnsActive     bool
	exit          bool
	skipped       map[netip.Prefix]bool
	cancel        context.CancelFunc
	done          chan struct{}
}

// Start creates the TUN device and starts routing tailnet traffic
// through it. It needs root.
func Start(ctx context.Context, m *mux.Mux, cfg *mux.Config) (*System, error) {
	osc, err := newOSConfig()
	if err != nil {
		return nil, err
	}
	fake := NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), filepath.Join(cfg.StateDir, "fakeip.json"))
	s := &System{m: m, cfg: cfg, os: osc, fake: fake,
		routes: map[netip.Prefix]bool{}, skipped: map[netip.Prefix]bool{}, done: make(chan struct{})}
	eng, err := s.newDevice()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.run(ctx, eng)
	m.OnChange(func() { s.reconcile(false) })
	s.reconcile(false)
	go s.watchNetwork(ctx)
	log.Printf("tun      %s  (fake IPs %s, DNS %s)", s.Interface(), fake.Prefix(), fake.DNS())
	return s, nil
}

// newDevice creates the TUN device and its engine, makes it current and
// addresses it. Routes and DNS are up to the caller.
func (s *System) newDevice() (*Engine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.cfg.TUN.Name
	if name == "" {
		name = defaultTUNName
	}
	dev, err := wgtun.CreateTUN(name, s.cfg.TUN.MTU)
	if err != nil {
		return nil, fmt.Errorf("create tun %q (are you root?): %w", name, err)
	}
	ifname, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, err
	}
	eng, err := NewEngine(s.m, dev, s.fake, s.cfg.TUN.MTU)
	if err != nil {
		dev.Close()
		return nil, err
	}
	if s.cfg.TUN.RealIPs == nil || *s.cfg.TUN.RealIPs {
		eng.routed = s.routed
	}
	if err := s.os.up(ifname, s.fake.Gateway(), s.fake.Prefix(), s.cfg.TUN.MTU); err != nil {
		eng.close()
		return nil, fmt.Errorf("configure %s: %w", ifname, err)
	}
	// Publish only a configured device. Every close (including the engine's
	// fatal-error path) must retire it before the OS can reuse its name.
	owned := &systemDevice{Device: dev, system: s}
	eng.dev = owned
	s.dev, s.ifname = owned, ifname
	return eng, nil
}

type systemDevice struct {
	wgtun.Device
	system *System
}

func (d *systemDevice) Close() error {
	s := d.system
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dev != d {
		return nil // already retired; a replacement may have the same name
	}
	s.dev = nil
	s.dnsActive = false
	s.exit = false
	s.domains = nil
	s.routedNow.Store(nil)
	s.searchDomains = nil
	clear(s.routes)
	// Per-interface cleanup must run while we still own the interface.
	s.closeErr = errors.Join(s.os.close(s.ifname), d.Device.Close())
	return s.closeErr
}

// run keeps a device running until ctx ends. When the engine stops on
// its own (the device failed or was destroyed), it builds a new device
// and puts routes and DNS back on it.
func (s *System) run(ctx context.Context, eng *Engine) {
	defer close(s.done)
	const minBackoff, maxBackoff = time.Second, 30 * time.Second
	backoff := minBackoff
	for {
		started := time.Now()
		err := eng.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		old := s.Interface()
		log.Printf("tun: %s stopped: %v; recreating it", old, err)
		if time.Since(started) > time.Minute {
			backoff = minBackoff
		}
		for {
			// Wait before every attempt so a device that dies right away
			// can't spin this loop.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, maxBackoff)
			if eng, err = s.newDevice(); err == nil {
				break
			}
			log.Printf("tun: recreate: %v", err)
		}
		s.reconcile(true)
		s.os.flushDNS()
		log.Printf("tun      %s  (replaces %s)", s.Interface(), old)
	}
}

// interfaceGone reports whether the OS no longer has the interface. Any
// error listing interfaces counts as "still there": under heavy network
// churn the listing itself can fail with ENOBUFS.
func interfaceGone(name string) bool {
	ifs, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifc := range ifs {
		if ifc.Name == name {
			return false
		}
	}
	return true
}

// Repair re-applies everything tailmux set up in the OS (interface
// address, every route, the DNS configuration) and flushes the OS DNS
// cache. The network watcher calls it after sleep and network changes;
// `tailmux repair` calls it by hand.
func (s *System) Repair(reason string) {
	s.mu.Lock()
	dev, ifname := s.dev, s.ifname
	if dev == nil {
		s.mu.Unlock()
		return
	}
	if interfaceGone(ifname) {
		// Nothing to re-apply to; run re-applies on the new device.
		s.mu.Unlock()
		log.Printf("tun: interface %s is gone", ifname)
		dev.Close()
		return
	}
	if reason != "" {
		log.Printf("tun: re-applying routes and DNS (%s)", reason)
	}
	if err := s.os.up(ifname, s.fake.Gateway(), s.fake.Prefix(), s.cfg.TUN.MTU); err != nil {
		log.Printf("tun: re-configure %s: %v", ifname, err)
	}
	s.reconcileLocked(true)
	s.mu.Unlock()
	s.os.flushDNS()
}

// watchNetwork notices the two events that leave the OS side stale: the
// machine sleeping (the wall clock jumps; Go's monotonic clock doesn't
// advance during sleep on macOS) and the network changing under us (new
// Wi-Fi, new address). It also checks every few minutes that routes and
// DNS are still in place.
func (s *System) watchNetwork(ctx context.Context) {
	const tick = 5 * time.Second
	t := time.NewTicker(tick)
	defer t.Stop()
	last := time.Now().Round(0)
	fp := netFingerprint(s.Interface())
	lastCheck := time.Now()
	var again <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-again:
			// Once tailnets have reconnected, flush again so no failure
			// cached in the meantime survives.
			again = nil
			s.Repair("")
		case <-t.C:
			now := time.Now().Round(0)
			slept := now.Sub(last) > 6*tick
			last = now
			ifname := s.Interface()
			nfp := netFingerprint(ifname)
			switch {
			case interfaceGone(ifname):
				s.Repair("")
			case slept:
				s.Repair("woke from sleep")
				again = time.After(20 * time.Second)
			case nfp != fp:
				time.Sleep(2 * time.Second) // let the new network settle
				s.Repair("network changed")
				again = time.After(20 * time.Second)
			case time.Since(lastCheck) > 5*time.Minute:
				lastCheck = time.Now()
				s.check()
			}
			fp = netFingerprint(s.Interface())
		}
	}
}

// check is the cheap periodic version of Repair: it only rewrites DNS
// and flushes when the OS lost the configuration.
func (s *System) check() {
	s.mu.Lock()
	domains, searchDomains, active := s.domains, s.searchDomains, s.dnsActive
	s.mu.Unlock()
	if active && !s.cfg.TUN.NoDNS && !s.os.dnsIntact(s.Interface(), domains, searchDomains) {
		log.Printf("tun: DNS configuration was lost; restoring it")
		s.Repair("")
		return
	}
	s.reconcile(true)
}

// netFingerprint summarizes the machine's other interfaces and their
// addresses; it changes when the network does.
func netFingerprint(skip string) string {
	ifs, _ := net.Interfaces()
	var parts []string
	for _, ifc := range ifs {
		if ifc.Name == skip || ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			parts = append(parts, ifc.Name+"="+a.String())
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// Interface is the current device's name. It changes if the device is
// recreated.
func (s *System) Interface() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ifname
}

func (s *System) FakeIPs() *FakeIPs { return s.fake }

// DNSActive reports whether the OS resolver sends tailnet domains here.
func (s *System) DNSActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dnsActive
}

// reconcile makes the OS routes and DNS domains match what the running
// tailnets claim. With force it re-applies routes and DNS even where it
// believes they are already in place, in case the OS dropped them.
// routed reports whether ip is inside a route sent into the TUN now.
func (s *System) routed(ip netip.Addr) bool {
	rs := s.routedNow.Load()
	return rs != nil && slices.ContainsFunc(*rs, func(p netip.Prefix) bool { return p.Contains(ip) })
}

func (s *System) publishRoutes() {
	rs := make([]netip.Prefix, 0, len(s.routes))
	for p := range s.routes {
		rs = append(rs, p)
	}
	s.routedNow.Store(&rs)
}

func (s *System) reconcile(force bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(force)
}

func (s *System) reconcileLocked(force bool) {
	if s.dev == nil {
		return
	}
	ifname := s.ifname
	if force {
		clear(s.skipped) // local networks may have changed
	}
	r := s.m.Router()
	prefixes, domains := r.Claims()
	searchDomains := r.SearchDomains()
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
	local := localPrefixes(ifname)
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
			if err := s.os.delRoute(ifname, p); err != nil {
				log.Printf("tun: remove route %s: %v", p, err)
			}
			delete(s.routes, p)
		}
	}
	for p := range want {
		if force || !s.routes[p] {
			if err := s.os.addRoute(ifname, p); err != nil {
				log.Printf("tun: add route %s: %v", p, err)
				continue
			}
			s.routes[p] = true
		}
	}
	s.publishRoutes()
	// With an exit node everything goes into the TUN (routes no tailnet
	// claims leave through the exit node), and so do all DNS lookups: "."
	// asks the OS to send every name here, where supported.
	exit := s.m.ExitNode() != nil
	if force || exit != s.exit {
		if err := s.os.setExit(ifname, exit); err != nil {
			log.Printf("tun: exit node routes: %v", err)
		} else if exit != s.exit {
			log.Printf("tun: %s", map[bool]string{true: "routing all traffic through the exit node", false: "exit node off; routing tailnet traffic only"}[exit])
		}
		s.exit = exit
	}
	if exit {
		domains = append(slices.Clone(domains), ".")
	}
	if s.cfg.TUN.NoDNS || (!force && slices.Equal(domains, s.domains) && slices.Equal(searchDomains, s.searchDomains)) {
		return
	}
	ok, err := s.os.setDNS(ifname, domains, searchDomains, s.fake.DNS())
	if err != nil {
		log.Printf("tun: dns: %v", err)
		return
	}
	searchChanged := !slices.Equal(searchDomains, s.searchDomains)
	s.domains, s.searchDomains, s.dnsActive = domains, searchDomains, ok
	if ok && searchChanged {
		s.os.flushDNS()
	}
	if ok && len(domains) > 0 {
		log.Printf("tun: resolving %s via %s", strings.Join(domains, ", "), s.fake.DNS())
	}
}

func (s *System) Close() error {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func localPrefixes(skip string) []netip.Prefix { return mux.LocalPrefixes(skip) }

// ExitActive reports whether all traffic is being routed into the TUN
// for an exit node.
func (s *System) ExitActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exit
}

func overlapsAny(p netip.Prefix, list []netip.Prefix) bool {
	for _, l := range list {
		if p.Overlaps(l) {
			return true
		}
	}
	return false
}
