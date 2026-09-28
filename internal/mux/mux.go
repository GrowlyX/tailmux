package mux

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Mux joins every configured tailnet at once and routes each connection
// to the tailnet that owns its destination.
type Mux struct {
	cfg      *Config
	tailnets []*Tailnet
	byName   map[string]*Tailnet
	router   atomic.Pointer[Router]
	direct   net.Dialer

	cacheMu sync.Mutex
	cache   map[string]cacheEntry

	// TUNStatus, if set, adds TUN details to /status.
	TUNStatus func() any

	listenMu  sync.Mutex
	listeners []func()
	verbose   bool
}

type cacheEntry struct {
	ips []netip.Addr
	exp time.Time
}

type Options struct {
	Verbose  bool
	MemStore bool
}

func New(cfg *Config, o Options) *Mux {
	m := &Mux{cfg: cfg, byName: map[string]*Tailnet{}, cache: map[string]cacheEntry{}, verbose: o.Verbose}
	m.direct.Timeout = 15 * time.Second
	for i, tc := range cfg.Tailnets {
		t := newTailnet(tc, i, tailnetOpts{stateDir: cfg.StateDir, verbose: o.Verbose, memStore: o.MemStore}, m.rebuild)
		m.tailnets = append(m.tailnets, t)
		m.byName[tc.Name] = t
	}
	m.rebuild()
	return m
}

// Start brings up every tailnet concurrently. Tailnets that need an
// interactive login print a URL and keep going in the background.
func (m *Mux) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make([]error, len(m.tailnets))
	for i, t := range m.tailnets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = t.start(ctx)
		}()
	}
	wg.Wait()
	for name := range m.loadDisabled() {
		if t := m.byName[name]; t != nil && t.lc != nil {
			if err := t.setEnabled(ctx, false); err != nil {
				errs = append(errs, err)
			}
		}
	}
	go m.sampleTraffic(ctx)
	return errors.Join(errs...)
}

func (m *Mux) Close() error {
	var errs []error
	for _, t := range m.tailnets {
		errs = append(errs, t.close())
	}
	return errors.Join(errs...)
}

func (m *Mux) rebuild() {
	snaps := make([]Snapshot, 0, len(m.tailnets))
	for _, t := range m.tailnets {
		snaps = append(snaps, t.Snapshot())
	}
	m.router.Store(NewRouter(snaps, m.cfg.pins()))
	m.cacheMu.Lock()
	clear(m.cache)
	m.cacheMu.Unlock()
	m.listenMu.Lock()
	ls := slices.Clone(m.listeners)
	m.listenMu.Unlock()
	for _, f := range ls {
		f()
	}
}

// OnChange registers f to run whenever the routing table changes.
func (m *Mux) OnChange(f func()) {
	m.listenMu.Lock()
	m.listeners = append(m.listeners, f)
	m.listenMu.Unlock()
}

func (m *Mux) Router() *Router { return m.router.Load() }

func (m *Mux) Tailnets() []*Tailnet { return m.tailnets }

func (m *Mux) Tailnet(name string) *Tailnet { return m.byName[name] }

// Target is where a destination ended up: a tailnet (with the decision
// that picked it) or the direct network.
type Target struct {
	Host     string       `json:"host"`
	Tailnet  string       `json:"tailnet,omitempty"` // empty means direct
	Decision Decision     `json:"decision"`
	IPs      []netip.Addr `json:"ips,omitempty"`
}

func (t Target) String() string {
	via := "direct"
	if t.Tailnet != "" {
		via = t.Tailnet
		if t.Decision.Kind != "" {
			via += "/" + string(t.Decision.Kind)
		}
	}
	return fmt.Sprintf("%s -> %v via %s", t.Host, t.IPs, via)
}

// Resolve decides which tailnet carries host and what addresses to dial.
func (m *Mux) Resolve(ctx context.Context, host string) (Target, error) {
	r := m.Router()
	tgt := Target{Host: host}
	d := r.RouteName(host)
	if d.OK() {
		tgt.Tailnet, tgt.Decision = d.Tailnet, d
		for _, s := range d.IPs {
			if ip, err := netip.ParseAddr(s); err == nil {
				tgt.IPs = append(tgt.IPs, ip)
			}
		}
		if ip, err := netip.ParseAddr(normName(host)); err == nil {
			tgt.IPs = []netip.Addr{ip}
		}
		if len(tgt.IPs) == 0 && d.Query != "" {
			tn := m.byName[d.Tailnet]
			ips, err := m.resolveIn(ctx, tn, d.Query)
			if err != nil {
				return tgt, err
			}
			tgt.IPs = ips
			// A split-DNS name can resolve somewhere its tailnet can't
			// reach (a public IP): then it isn't really in the tailnet.
			if !slices.ContainsFunc(ips, tn.owns) {
				tgt.Tailnet = ""
				tgt.Decision.Kind += "-offnet"
			}
		}
		sortV4First(tgt.IPs)
		return tgt, nil
	}
	if ip, err := netip.ParseAddr(normName(host)); err == nil {
		tgt.IPs = []netip.Addr{ip}
		return tgt, nil
	}

	// Nobody claims the name. Resolve it normally; the answer may still
	// land inside some tailnet's subnet (internal names in public DNS).
	ips, err := m.lookupSystem(ctx, host)
	if err != nil {
		return tgt, err
	}
	sortV4First(ips)
	for _, ip := range ips {
		if d := r.RouteIP(ip); d.OK() {
			tgt.Tailnet, tgt.Decision = d.Tailnet, d
			tgt.IPs = []netip.Addr{ip}
			return tgt, nil
		}
	}
	tgt.IPs = ips
	return tgt, nil
}

func (m *Mux) resolveIn(ctx context.Context, t *Tailnet, name string) ([]netip.Addr, error) {
	key := t.cfg.Name + "|" + name
	if ips, ok := m.cached(key); ok {
		return ips, nil
	}
	ips, err := t.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	m.store(key, ips)
	return ips, nil
}

func (m *Mux) lookupSystem(ctx context.Context, host string) ([]netip.Addr, error) {
	key := "|" + host
	if ips, ok := m.cached(key); ok {
		return ips, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	m.store(key, ips)
	return ips, nil
}

func (m *Mux) cached(key string) ([]netip.Addr, bool) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	e, ok := m.cache[key]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.ips, true
}

func (m *Mux) store(key string, ips []netip.Addr) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.cache[key] = cacheEntry{ips: ips, exp: time.Now().Add(30 * time.Second)}
}

func sortV4First(ips []netip.Addr) {
	slices.SortStableFunc(ips, func(a, b netip.Addr) int {
		switch {
		case a.Is4() && !b.Is4():
			return -1
		case !a.Is4() && b.Is4():
			return 1
		}
		return 0
	})
}

var ErrDirectDisabled = errors.New("destination is not in any tailnet and direct dialing is disabled")

// Dial connects to addr ("host:port") through whichever tailnet owns it,
// or directly if none does and the config allows it.
func (m *Mux) Dial(ctx context.Context, network, addr string) (net.Conn, Target, error) {
	return m.dial(ctx, network, addr, *m.cfg.Direct)
}

var ErrNotInTailnet = errors.New("destination is not in any tailnet")

// DialTailnet is Dial without the direct fallback. The TUN device uses it:
// its traffic was routed here by the OS, so dialing it "directly" would
// loop straight back into the TUN.
func (m *Mux) DialTailnet(ctx context.Context, network, addr string) (net.Conn, Target, error) {
	return m.dial(ctx, network, addr, false)
}

func (m *Mux) dial(ctx context.Context, network, addr string, allowDirect bool) (net.Conn, Target, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, Target{}, err
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, Target{}, fmt.Errorf("bad port %q", port)
	}
	tgt, err := m.Resolve(ctx, host)
	if err != nil {
		return nil, tgt, err
	}
	dial := m.direct.DialContext
	if tgt.Tailnet != "" {
		dial = m.byName[tgt.Tailnet].Dial
	} else if !allowDirect {
		if !*m.cfg.Direct {
			return nil, tgt, ErrDirectDisabled
		}
		return nil, tgt, ErrNotInTailnet
	}
	var lastErr error
	for _, ip := range tgt.IPs {
		c, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, tgt, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s: no addresses", host)
	}
	return nil, tgt, lastErr
}

// LogDial logs a connection the way every frontend does.
func (m *Mux) LogDial(kind string, tgt Target, err error) {
	if err == nil && tgt.Tailnet == "" && !m.verbose {
		return
	}
	if err != nil {
		log.Printf("%s %s: %v", kind, tgt, err)
		return
	}
	log.Printf("%s %s", kind, tgt)
}
