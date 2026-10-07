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

// Version is the running build, reported in /status.
var Version = "dev"

// Mux joins every configured tailnet at once and routes each connection
// to the tailnet that owns its destination.
type Mux struct {
	cfg      *Config
	opts     Options
	tmu      sync.RWMutex // guards tailnets, byName, ctx
	tailnets []*Tailnet
	byName   map[string]*Tailnet
	ctx      context.Context // set by Start; tailnets added later start with it
	router   atomic.Pointer[Router]
	exitCfg  atomic.Pointer[ExitNodeConfig]

	localNets localNets

	// ConfigPath, if set, is where tailnet and settings changes made
	// through the API are saved.
	ConfigPath string
	// Restart, if set, restarts the daemon (to apply settings).
	Restart func()
	direct  net.Dialer

	cacheMu sync.Mutex
	cache   map[string]cacheEntry

	// TUNStatus, if set, adds TUN details to /status.
	TUNStatus func() any
	// UpdateStatus and TriggerUpdate, if set, report on and start an
	// update (/status and POST /update).
	UpdateStatus  func() any
	TriggerUpdate func() error
	// Repair, if set, re-applies OS routes/DNS (POST /repair).
	Repair func()

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
	m := &Mux{cfg: cfg, opts: o, byName: map[string]*Tailnet{}, cache: map[string]cacheEntry{}, verbose: o.Verbose}
	m.direct.Timeout = 15 * time.Second
	for i, tc := range cfg.Tailnets {
		t := newTailnet(tc, i, tailnetOpts{stateDir: cfg.StateDir, verbose: o.Verbose, memStore: o.MemStore}, m.rebuild)
		if e := cfg.ExitNode; e != nil && e.Tailnet == tc.Name {
			t.exitWant = e.Node
		}
		m.tailnets = append(m.tailnets, t)
		m.byName[tc.Name] = t
	}
	if cfg.ExitNode != nil {
		e := *cfg.ExitNode
		m.exitCfg.Store(&e)
	}
	m.rebuild()
	return m
}

// Start brings up every tailnet concurrently. Tailnets that need an
// interactive login print a URL and keep going in the background.
func (m *Mux) Start(ctx context.Context) error {
	m.tmu.Lock()
	m.ctx = ctx
	m.tmu.Unlock()
	list := m.list()
	var wg sync.WaitGroup
	errs := make([]error, len(list))
	for i, t := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = t.start(ctx)
		}()
	}
	wg.Wait()
	for name := range m.loadDisabled() {
		if t := m.get(name); t != nil && t.lc != nil {
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
	for _, t := range m.list() {
		errs = append(errs, t.close())
	}
	return errors.Join(errs...)
}

func (m *Mux) rebuild() {
	list := m.list()
	snaps := make([]Snapshot, 0, len(list))
	for _, t := range list {
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

func (m *Mux) Tailnets() []*Tailnet { return m.list() }

func (m *Mux) Tailnet(name string) *Tailnet { return m.get(name) }

func (m *Mux) list() []*Tailnet {
	m.tmu.RLock()
	defer m.tmu.RUnlock()
	return slices.Clone(m.tailnets)
}

func (m *Mux) get(name string) *Tailnet {
	m.tmu.RLock()
	defer m.tmu.RUnlock()
	return m.byName[name]
}

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
			tn := m.get(d.Tailnet)
			if tn == nil {
				return tgt, fmt.Errorf("tailnet %s was removed", d.Tailnet)
			}
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

	// Nobody claims the name. Resolve it normally (through the exit node,
	// if there is one); the answer may still land inside some tailnet's
	// subnet (internal names in public DNS).
	ips, err := m.lookupUnclaimed(ctx, host)
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

// lookupUnclaimed resolves a name no tailnet claims: with the exit
// node's resolver when one is set, so lookups don't leak to the local
// network and answers fit where traffic comes out, else the system's.
func (m *Mux) lookupUnclaimed(ctx context.Context, host string) ([]netip.Addr, error) {
	if m.ExitNode() == nil || localName(host) {
		return m.lookupSystem(ctx, host)
	}
	t, err := m.exitTailnet()
	if err != nil {
		return nil, err
	}
	return m.resolveIn(ctx, t, host)
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

// DialTailnet is Dial without the direct fallback (the exit node still
// applies). The TUN device uses it: its traffic was routed here by the
// OS, so dialing it "directly" would loop straight back into the TUN.
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
	remote := slices.DeleteFunc(slices.Clone(tgt.IPs), m.isLocal)
	if tgt.Tailnet != "" {
		tn := m.get(tgt.Tailnet)
		if tn == nil {
			return nil, tgt, fmt.Errorf("tailnet %s was removed", tgt.Tailnet)
		}
		dial = tn.Dial
	} else if e := m.ExitNode(); e != nil && len(remote) > 0 {
		tn, err := m.exitTailnet()
		if err != nil {
			return nil, tgt, err
		}
		dial = tn.Dial
		tgt.IPs = remote
		tgt.Tailnet = e.Tailnet
		tgt.Decision = Decision{Tailnet: e.Tailnet, Kind: KindExit, Peer: e.Node}
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

// Ping sends an ICMP echo to host through the tailnet that owns it, or
// through the exit node for anything else, and waits for the answer.
func (m *Mux) Ping(ctx context.Context, host string) error {
	tgt, err := m.Resolve(ctx, host)
	if err != nil {
		return err
	}
	if len(tgt.IPs) == 0 {
		return fmt.Errorf("%s: no addresses", host)
	}
	ip := tgt.IPs[0]
	var t *Tailnet
	switch {
	case tgt.Tailnet != "":
		t = m.get(tgt.Tailnet)
	case m.ExitNode() != nil && !m.isLocal(ip):
		if t, err = m.exitTailnet(); err != nil {
			return err
		}
	}
	if t == nil || !t.Enabled() {
		return ErrNotInTailnet
	}
	return t.Ping(ctx, ip)
}

// LogDial logs a connection the way every frontend does.
func (m *Mux) LogDial(kind string, tgt Target, err error) {
	if err == nil && (tgt.Tailnet == "" || tgt.Decision.Kind == KindExit) && !m.verbose {
		return
	}
	if err != nil {
		log.Printf("%s %s: %v", kind, tgt, err)
		return
	}
	log.Printf("%s %s", kind, tgt)
}
