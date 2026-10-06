package mux

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

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
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

	startMu sync.Mutex // serializes initialization and cleanup
	started bool       // tsnet cleans up its own failed Start

	mu      sync.Mutex
	snap    Snapshot
	state   string
	authURL string
	selfIPs []netip.Addr
	err     string
	// split-DNS domain -> resolver addresses, for names we resolve ourselves
	splitRes map[string][]netip.AddrPort

	lock *LockStatus // nil until known, or when Tailnet Lock is off

	exitWant string               // exit node to use, as configured; "" for none
	exitID   tailcfg.StableNodeID // exit node the node is set to use
	exitErr  string               // why exitWant isn't in use
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
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if err := t.srv.Start(); err != nil {
		return fmt.Errorf("%s: %w", t.cfg.Name, err)
	}
	t.started = true
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
	// With Tailnet Lock on, a device nobody has signed yet is logged in
	// and "Running", but every other device drops its traffic. Don't claim
	// anything for it until it's signed.
	var lock *LockStatus
	if st.BackendState == ipn.Running.String() {
		lock = t.lockStatus(ctx)
		if lock.needsSignature() {
			snap.Running = false
		}
	}
	// Starting after having been Running is a reconnect (sleep, network
	// change), not a logout: keep claiming.
	t.mu.Lock()
	wasActive := t.snap.Running || t.snap.Reconnecting
	t.mu.Unlock()
	snap.Reconnecting = !snap.Running && t.Enabled() && wasActive && st.BackendState == ipn.Starting.String()
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
		p.ID = string(ps.ID)
		p.Exit = ps.ExitNodeOption
		if l := ps.Location; l != nil {
			p.Location = Location{Country: l.Country, CountryCode: l.CountryCode, City: l.City, CityCode: l.CityCode, Priority: l.Priority}
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
				if len(res) == 0 || !usefulSplitDomain(dom, snap.Suffix) {
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
	if lock.needsSignature() && !t.lock.needsSignature() {
		t.logf("Tailnet Lock: this device needs a signature; on a signing device run: %s", lock.SignCommand)
	}
	t.lock = lock
	if st.ExitNodeStatus != nil {
		t.exitID = st.ExitNodeStatus.ID
	} else {
		t.exitID = ""
	}
	t.mu.Unlock()
	if t.syncExitNode(ctx, snap) {
		changed = true
	}
	if changed && t.onChange != nil {
		t.onChange()
	}
}

// syncExitNode points the node's exit node preference at exitWant (or
// clears it), and reports whether that changed anything.
func (t *Tailnet) syncExitNode(ctx context.Context, snap Snapshot) bool {
	t.mu.Lock()
	want, cur := t.exitWant, t.exitID
	t.mu.Unlock()
	var id tailcfg.StableNodeID
	var why string
	if want != "" {
		switch p := findPeer(snap.Peers, want); {
		case p == nil && !snap.Running:
			why = "tailnet " + t.cfg.Name + " is not running"
		case p == nil:
			why = fmt.Sprintf("no device %q in tailnet %s", want, t.cfg.Name)
		case !p.Exit:
			why = fmt.Sprintf("%s does not offer itself as an exit node", p.Name)
		default:
			// Not refused when it looks offline: Mullvad's nodes don't
			// report presence. The status shows it either way.
			id = tailcfg.StableNodeID(p.ID)
		}
	}
	t.mu.Lock()
	t.exitErr = why
	t.mu.Unlock()
	if id == cur || (id == "" && want != "" && !snap.Running) {
		// Nothing to do, or not running yet: keep what's there until we
		// know which node the name means.
		return false
	}
	if _, err := t.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:         ipn.Prefs{ExitNodeID: id},
		ExitNodeIDSet: true,
		ExitNodeIPSet: true, // clears any exit node set by IP
	}); err != nil {
		t.mu.Lock()
		t.exitErr = "set exit node: " + err.Error()
		t.mu.Unlock()
		return false
	}
	t.mu.Lock()
	t.exitID = id
	t.mu.Unlock()
	if id != "" {
		t.logf("exit node: %s", want)
	} else {
		t.logf("exit node: none")
	}
	return true
}

// findPeer finds a device by stable ID, MagicDNS name, short name or
// Tailscale IP.
func findPeer(peers []Peer, spec string) *Peer {
	spec = normName(spec)
	for i := range peers {
		p := &peers[i]
		if strings.EqualFold(p.ID, spec) || strings.EqualFold(p.FQDN, spec) || strings.EqualFold(p.Name, spec) {
			return p
		}
		for _, ip := range p.IPs {
			if ip.String() == spec {
				return p
			}
		}
	}
	return nil
}

// setExitNode makes this tailnet use spec as its exit node ("" for none)
// and applies it right away.
func (t *Tailnet) setExitNode(ctx context.Context, spec string) {
	t.mu.Lock()
	t.exitWant = spec
	t.mu.Unlock()
	if t.lc != nil {
		t.refresh(ctx)
	}
}

// exitReady reports whether traffic sent to this tailnet's Dial with a
// non-tailnet destination leaves through the configured exit node.
func (t *Tailnet) exitReady() (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exitWant == "" {
		return false, "no exit node"
	}
	if !t.snap.Running || !t.Enabled() {
		return false, "tailnet " + t.cfg.Name + " is not running"
	}
	if t.exitErr != "" {
		return false, t.exitErr
	}
	if t.exitID == "" {
		return false, "exit node not set yet"
	}
	return true, ""
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

// Ping sends an ICMP echo through the tailnet (to a peer or anything
// behind a subnet router) and waits for the answer.
func (t *Tailnet) Ping(ctx context.Context, ip netip.Addr) error {
	res, err := t.lc.Ping(ctx, ip, tailcfg.PingICMP)
	if err != nil {
		return err
	}
	if res.Err != "" {
		return errors.New(res.Err)
	}
	return nil
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
	if res := t.splitResolvers(name); len(res) > 0 && !t.ownName(name) {
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
			firstErr = fmt.Errorf("%s: %w in tailnet %s", name, ErrNoSuchHost, t.cfg.Name)
		}
		return nil, firstErr
	}
	return out, nil
}

// ownName reports whether name is under this tailnet's MagicDNS suffix;
// those always go to MagicDNS, whatever split routes cover a parent.
func (t *Tailnet) ownName(name string) bool {
	suf := t.Snapshot().Suffix
	return suf != "" && hasSuffixDomain(normName(name), suf)
}

// usefulSplitDomain filters a tailnet's DNS routes down to the ones that
// mean "this tailnet owns these names". Dropped: empty (MagicDNS-internal
// ExtraRecords), reverse zones, and anything at or above the MagicDNS
// suffix, like the "ts.net" route Tailscale adds for its public names,
// which every tailnet has and none owns.
func usefulSplitDomain(dom, suffix string) bool {
	switch {
	case dom == "" || strings.HasSuffix(dom, ".arpa"):
		return false
	case suffix != "" && hasSuffixDomain(suffix, dom):
		return false
	case dom == "ts.net" || dom == "beta.tailscale.net":
		return false
	}
	return true
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

// Owns reports whether ip is reachable through this tailnet.
func (t *Tailnet) Owns(ip netip.Addr) bool { return t.owns(ip) }

func (t *Tailnet) owns(ip netip.Addr) bool {
	d := NewRouter([]Snapshot{t.Snapshot()}, Pins{}).RouteIP(ip)
	return d.OK() || tsaddr.IsTailscaleIP(ip)
}

// ErrNoSuchHost is a definite "this name doesn't exist" from a tailnet's
// DNS, as opposed to a lookup that failed (tailnet reconnecting, resolver
// unreachable), which is worth retrying.
var ErrNoSuchHost = errors.New("no such host")

// IsNotFound reports whether err means the name definitely doesn't exist.
func IsNotFound(err error) bool {
	if errors.Is(err, ErrNoSuchHost) {
		return true
	}
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
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
		if h.RCode == dnsmessage.RCodeNameError {
			return nil, fmt.Errorf("dns: %w", ErrNoSuchHost)
		}
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
	// Lock is set when the tailnet uses Tailnet Lock.
	Lock *LockStatus `json:"lock,omitempty"`
}

func (t *Tailnet) Status() TailnetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TailnetStatus{Name: t.cfg.Name, Enabled: t.Enabled(), State: t.state, AuthURL: t.authURL, Suffix: t.snap.Suffix, Peers: len(t.snap.Peers), SplitDNS: t.snap.SplitDNS, Error: t.err, Lock: t.lock}
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

func (t *Tailnet) close() error {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if !t.started {
		return nil
	}
	return t.srv.Close()
}
