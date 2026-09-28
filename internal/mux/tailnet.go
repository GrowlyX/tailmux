package mux

import (
	"context"
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

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

// Tailnet is one userspace Tailscale node joined to one tailnet. Many of
// them live in one process, which is what the official client can't do.
type Tailnet struct {
	cfg      TailnetConfig
	priority int
	srv      *tsnet.Server
	lc       *local.Client
	onChange func()
	traffic  traffic
	disabled atomic.Bool

	mu      sync.Mutex
	snap    Snapshot
	state   string
	authURL string
	selfIPs []netip.Addr
	err     string
	// split-DNS domain -> resolver addresses, for names we resolve ourselves
	splitRes map[string][]netip.AddrPort
}

type tailnetOpts struct {
	stateDir string
	verbose  bool
	memStore bool // tests: keep node state in memory
}

func newTailnet(cfg TailnetConfig, priority int, o tailnetOpts, onChange func()) *Tailnet {
	t := &Tailnet{cfg: cfg, priority: priority, onChange: onChange}
	t.snap = Snapshot{Name: cfg.Name, Priority: priority}
	t.srv = &tsnet.Server{
		Dir:        filepath.Join(o.stateDir, cfg.Name),
		Hostname:   cfg.Hostname,
		AuthKey:    cfg.AuthKey,
		ControlURL: cfg.ControlURL,
		Ephemeral:  cfg.Ephemeral,
		UserLogf:   t.logf,
		Logf:       logger.Discard,
	}
	if o.verbose {
		t.srv.Logf = t.logf
	}
	if o.memStore {
		t.srv.Store = new(mem.Store)
	}
	return t
}

func (t *Tailnet) logf(format string, args ...any) {
	log.Printf("["+t.cfg.Name+"] "+format, args...)
}

func (t *Tailnet) start(ctx context.Context) error {
	if err := t.srv.Start(); err != nil {
		return fmt.Errorf("%s: %w", t.cfg.Name, err)
	}
	lc, err := t.srv.LocalClient()
	if err != nil {
		return err
	}
	t.lc = lc
	// Accept subnet routes; that's the whole point.
	if _, err := lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:       ipn.Prefs{RouteAll: true},
		RouteAllSet: true,
	}); err != nil {
		return fmt.Errorf("%s: accept routes: %w", t.cfg.Name, err)
	}
	go t.watch(ctx)
	return nil
}

// watch refreshes the snapshot whenever the IPN bus says something
// changed, and every few seconds regardless.
func (t *Tailnet) watch(ctx context.Context) {
	kick := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			w, err := t.lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			for {
				n, err := w.Next()
				if err != nil {
					break
				}
				if n.BrowseToURL != nil && *n.BrowseToURL != "" {
					t.logf("login required: %s", *n.BrowseToURL)
				}
				select {
				case kick <- struct{}{}:
				default:
				}
			}
			w.Close()
		}
	}()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		t.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-kick:
			time.Sleep(100 * time.Millisecond) // coalesce bursts
		case <-tick.C:
		}
	}
}

func (t *Tailnet) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := t.lc.Status(ctx)
	if err != nil {
		t.mu.Lock()
		t.err = err.Error()
		t.mu.Unlock()
		return
	}
	snap := Snapshot{Name: t.cfg.Name, Priority: t.priority, Running: st.BackendState == ipn.Running.String() && t.Enabled()}
	if st.CurrentTailnet != nil {
		snap.Suffix = strings.Trim(st.CurrentTailnet.MagicDNSSuffix, ".")
	}
	for _, ps := range st.Peer {
		p := Peer{
			FQDN:   strings.TrimSuffix(ps.DNSName, "."),
			IPs:    ps.TailscaleIPs,
			Online: ps.Online,
		}
		p.Name, _, _ = strings.Cut(p.FQDN, ".")
		if p.Name == "" {
			p.Name = strings.ToLower(ps.HostName)
		}
		if ps.PrimaryRoutes != nil {
			for _, r := range ps.PrimaryRoutes.All() {
				if r.Bits() > 0 { // skip exit-node default routes
					p.Routes = append(p.Routes, r)
				}
			}
		}
		snap.Peers = append(snap.Peers, p)
	}
	splitRes := map[string][]netip.AddrPort{}
	dnsOK := false
	if snap.Running {
		if dc, err := t.lc.DNSConfig(ctx); err == nil && dc != nil {
			dnsOK = true
			for dom, res := range dc.Routes {
				dom = normName(dom)
				// Empty resolver lists are MagicDNS-internal (ExtraRecords);
				// reverse zones aren't useful for forward routing.
				if len(res) == 0 || strings.HasSuffix(dom, ".arpa") {
					continue
				}
				snap.SplitDNS = append(snap.SplitDNS, dom)
				for _, r := range res {
					if ap, ok := r.IPPort(); ok {
						splitRes[dom] = append(splitRes[dom], ap)
					}
				}
			}
		}
	}

	slices.SortFunc(snap.Peers, func(a, b Peer) int { return strings.Compare(a.FQDN, b.FQDN) })
	slices.Sort(snap.SplitDNS)

	t.mu.Lock()
	if snap.Running && !dnsOK {
		// A slow DNS-config fetch shouldn't drop split-DNS routing;
		// keep the last known config until the next refresh.
		snap.SplitDNS, splitRes = t.snap.SplitDNS, t.splitRes
	}
	changed := !snapEqual(t.snap, snap)
	t.snap = snap
	t.state = st.BackendState
	t.authURL = st.AuthURL
	t.selfIPs = st.TailscaleIPs
	t.splitRes = splitRes
	t.err = ""
	t.mu.Unlock()
	if changed && t.onChange != nil {
		t.onChange()
	}
}

func snapEqual(a, b Snapshot) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func (t *Tailnet) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snap
}

func (t *Tailnet) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if !t.Enabled() {
		return nil, fmt.Errorf("tailnet %s is disabled", t.cfg.Name)
	}
	c, err := t.srv.Dial(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	t.traffic.conns.Add(1)
	return &countingConn{Conn: c, tr: &t.traffic}, nil
}

func (t *Tailnet) Enabled() bool { return !t.disabled.Load() }

func (t *Tailnet) setEnabled(ctx context.Context, on bool) error {
	t.disabled.Store(!on)
	if t.lc != nil {
		if _, err := t.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
			Prefs:          ipn.Prefs{WantRunning: on},
			WantRunningSet: true,
		}); err != nil {
			return err
		}
		t.refresh(ctx)
	}
	if t.onChange != nil {
		t.onChange()
	}
	t.logf("%s", map[bool]string{true: "enabled", false: "disabled"}[on])
	return nil
}

// Resolve looks name up the way this tailnet's own client would.
// Split-DNS names go straight to the tailnet's resolvers, dialed through
// this node: tsnet's built-in forwarder sends UDP over the host network,
// which can't reach a resolver that only exists inside the tailnet.
// Everything else (MagicDNS, ExtraRecords) uses the node's resolver.
func (t *Tailnet) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	if res := t.splitResolvers(name); len(res) > 0 {
		return t.resolveVia(ctx, name, res)
	}
	var out []netip.Addr
	var firstErr error
	for _, qt := range []string{"A", "AAAA"} {
		b, _, err := t.lc.QueryDNS(ctx, name, qt)
		if err != nil {
			firstErr = cmpErr(firstErr, err)
			continue
		}
		ips, err := parseDNSAddrs(b)
		if err != nil {
			firstErr = cmpErr(firstErr, err)
			continue
		}
		out = append(out, ips...)
	}
	if len(out) == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: no such host in tailnet %s", name, t.cfg.Name)
		}
		return nil, firstErr
	}
	return out, nil
}

func (t *Tailnet) splitResolvers(name string) []netip.AddrPort {
	name = normName(name)
	t.mu.Lock()
	defer t.mu.Unlock()
	var best string
	for dom := range t.splitRes {
		if hasSuffixDomain(name, dom) && len(dom) > len(best) {
			best = dom
		}
	}
	return t.splitRes[best]
}

func (t *Tailnet) resolveVia(ctx context.Context, name string, resolvers []netip.AddrPort) ([]netip.Addr, error) {
	var lastErr error
	for _, ap := range resolvers {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if t.owns(ap.Addr()) {
				return t.srv.Dial(ctx, network, ap.String())
			}
			var d net.Dialer
			return d.DialContext(ctx, network, ap.String())
		}}
		ips, err := r.LookupNetIP(ctx, "ip", name)
		if err == nil && len(ips) > 0 {
			for i := range ips {
				ips[i] = ips[i].Unmap()
			}
			return ips, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("%s via %s split DNS: %w", name, t.cfg.Name, lastErr)
}

// owns reports whether ip is reachable through this tailnet.
func (t *Tailnet) owns(ip netip.Addr) bool {
	d := NewRouter([]Snapshot{t.Snapshot()}, Pins{}).RouteIP(ip)
	return d.OK() || tsaddr.IsTailscaleIP(ip)
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func parseDNSAddrs(b []byte) ([]netip.Addr, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return nil, err
	}
	if h.RCode != dnsmessage.RCodeSuccess {
		return nil, fmt.Errorf("dns: %v", h.RCode)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for {
		ah, err := p.AnswerHeader()
		if err == dnsmessage.ErrSectionDone {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch ah.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err != nil {
				return out, err
			}
			out = append(out, netip.AddrFrom4(r.A))
		case dnsmessage.TypeAAAA:
			r, err := p.AAAAResource()
			if err != nil {
				return out, err
			}
			out = append(out, netip.AddrFrom16(r.AAAA))
		default:
			if err := p.SkipAnswer(); err != nil {
				return out, err
			}
		}
	}
}

type TailnetStatus struct {
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	State    string   `json:"state"`
	AuthURL  string   `json:"auth_url,omitempty"`
	Suffix   string   `json:"suffix,omitempty"`
	SelfIPs  []string `json:"self_ips,omitempty"`
	Peers    int      `json:"peers"`
	Online   int      `json:"online"`
	Routes   []string `json:"routes,omitempty"`
	SplitDNS []string `json:"split_dns,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (t *Tailnet) Status() TailnetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TailnetStatus{Name: t.cfg.Name, Enabled: t.Enabled(), State: t.state, AuthURL: t.authURL, Suffix: t.snap.Suffix, Peers: len(t.snap.Peers), SplitDNS: t.snap.SplitDNS, Error: t.err}
	for _, ip := range t.selfIPs {
		s.SelfIPs = append(s.SelfIPs, ip.String())
	}
	for _, p := range t.snap.Peers {
		if p.Online {
			s.Online++
		}
		for _, r := range p.Routes {
			s.Routes = append(s.Routes, r.String()+" via "+p.Name)
		}
	}
	return s
}

func (t *Tailnet) close() error { return t.srv.Close() }
