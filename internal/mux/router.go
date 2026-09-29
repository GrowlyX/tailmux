package mux

import (
	"cmp"
	"net/netip"
	"slices"
	"strings"
)

// Snapshot is a point-in-time view of one tailnet, derived from the
// node's status and DNS config. Router decisions are pure functions of
// a set of snapshots, which keeps them unit-testable.
type Snapshot struct {
	Name     string   // configured name, e.g. "work"
	Priority int      // lower wins ties; config order
	Suffix   string   // MagicDNS suffix without dots, e.g. "tail1234.ts.net"
	SplitDNS []string // split-DNS domains the tailnet's resolver owns
	Peers    []Peer
	Running  bool
}

type Peer struct {
	Name   string // short hostname, first label of FQDN
	FQDN   string // without trailing dot
	IPs    []netip.Addr
	Routes []netip.Prefix // advertised subnet routes (primary only, no exit routes)
	Online bool
}

// Pin forces a prefix or domain suffix to a tailnet, overriding the
// automatic choice when several tailnets claim the same destination.
type Pins struct {
	Prefixes map[netip.Prefix]string
	Domains  map[string]string
}

type Kind string

const (
	KindPeerIP   Kind = "peer-ip"   // exact tailscale IP of a peer
	KindSubnet   Kind = "subnet"    // inside an advertised subnet route
	KindMagicDNS Kind = "magicdns"  // name under the tailnet's MagicDNS suffix
	KindAlias    Kind = "alias"     // host.<tailnet-name>
	KindShort    Kind = "shortname" // bare hostname matched against peers
	KindSplitDNS Kind = "splitdns"  // domain served by the tailnet's split DNS
	KindPinned   Kind = "pinned"
)

// Decision says which tailnet should carry a destination.
type Decision struct {
	Tailnet   string   `json:"tailnet,omitempty"`
	Kind      Kind     `json:"kind,omitempty"`
	Peer      string   `json:"peer,omitempty"`      // matched peer, if any
	Prefix    string   `json:"prefix,omitempty"`    // matched route, for IP decisions
	Query     string   `json:"query,omitempty"`     // name to resolve inside the tailnet
	IPs       []string `json:"ips,omitempty"`       // known addresses (peer matches)
	Contested []string `json:"contested,omitempty"` // other tailnets that also matched
}

func (d Decision) OK() bool { return d.Tailnet != "" }

type Router struct {
	nets []Snapshot // sorted by priority
	pins Pins
}

func NewRouter(snaps []Snapshot, pins Pins) *Router {
	nets := slices.Clone(snaps)
	slices.SortStableFunc(nets, func(a, b Snapshot) int { return cmp.Compare(a.Priority, b.Priority) })
	return &Router{nets: nets, pins: pins}
}

func (r *Router) Snapshots() []Snapshot { return r.nets }

type ipCandidate struct {
	tailnet  string
	priority int
	bits     int
	kind     Kind
	peer     string
	prefix   netip.Prefix
	online   bool
}

// RouteIP picks the tailnet for an IP: longest prefix wins (a peer's own
// address is a /32 and beats any subnet), then an online peer beats an
// offline one, then config order.
func (r *Router) RouteIP(ip netip.Addr) Decision {
	ip = ip.Unmap()
	if pinned, ok := r.pinnedPrefix(ip); ok {
		return pinned
	}
	var cands []ipCandidate
	for _, n := range r.nets {
		if !n.Running {
			continue
		}
		var best *ipCandidate
		consider := func(c ipCandidate) {
			if best == nil || c.bits > best.bits || (c.bits == best.bits && c.online && !best.online) {
				best = &c
			}
		}
		for _, p := range n.Peers {
			for _, a := range p.IPs {
				if a == ip {
					consider(ipCandidate{n.Name, n.Priority, ip.BitLen(), KindPeerIP, p.Name, netip.PrefixFrom(a, a.BitLen()), p.Online})
				}
			}
			for _, pfx := range p.Routes {
				if pfx.Contains(ip) {
					consider(ipCandidate{n.Name, n.Priority, pfx.Bits(), KindSubnet, p.Name, pfx, p.Online})
				}
			}
		}
		if best != nil {
			cands = append(cands, *best)
		}
	}
	if len(cands) == 0 {
		return Decision{}
	}
	slices.SortStableFunc(cands, func(a, b ipCandidate) int {
		if c := cmp.Compare(b.bits, a.bits); c != 0 {
			return c
		}
		if a.online != b.online {
			if a.online {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.priority, b.priority)
	})
	w := cands[0]
	d := Decision{Tailnet: w.tailnet, Kind: w.kind, Peer: w.peer, Prefix: w.prefix.String()}
	for _, c := range cands[1:] {
		if c.bits == w.bits {
			d.Contested = append(d.Contested, c.tailnet)
		}
	}
	return d
}

func (r *Router) pinnedPrefix(ip netip.Addr) (Decision, bool) {
	var best netip.Prefix
	var tn string
	for p, t := range r.pins.Prefixes {
		if p.Contains(ip) && (!best.IsValid() || p.Bits() > best.Bits()) {
			best, tn = p, t
		}
	}
	if n := r.find(tn); n == nil || !n.Running {
		return Decision{}, false // a pin to a stopped tailnet falls back to normal routing
	}
	return Decision{Tailnet: tn, Kind: KindPinned, Prefix: best.String()}, true
}

func (r *Router) find(name string) *Snapshot {
	for i := range r.nets {
		if r.nets[i].Name == name {
			return &r.nets[i]
		}
	}
	return nil
}

func normName(h string) string { return strings.ToLower(strings.TrimSuffix(h, ".")) }

func hasSuffixDomain(name, suffix string) bool {
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}

// RouteName picks the tailnet for a hostname. It returns a zero Decision
// for names no tailnet claims; the caller resolves those with the system
// resolver and feeds the result to RouteIP.
func (r *Router) RouteName(host string) Decision {
	name := normName(host)
	if ip, err := netip.ParseAddr(name); err == nil {
		return r.RouteIP(ip)
	}

	// Domain pins, longest suffix first.
	var pinDom, pinNet string
	for dom, tn := range r.pins.Domains {
		dom = normName(dom)
		if hasSuffixDomain(name, dom) && len(dom) > len(pinDom) {
			pinDom, pinNet = dom, tn
		}
	}
	if n := r.find(pinNet); n != nil && n.Running {
		d := r.nameIn(n, name)
		d.Kind = KindPinned
		return d
	}

	// host.<tailnet-name> alias, e.g. "db.home".
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		if n := r.find(name[i+1:]); n != nil {
			d := r.nameIn(n, name[:i])
			d.Kind = KindAlias
			return d
		}
	}

	// Full MagicDNS name. Suffixes are unique per tailnet.
	for i := range r.nets {
		n := &r.nets[i]
		if n.Suffix != "" && hasSuffixDomain(name, n.Suffix) {
			d := r.nameIn(n, name)
			d.Kind = KindMagicDNS
			return d
		}
	}

	// Split DNS, longest domain wins, then priority.
	var best *Snapshot
	var bestDom string
	var contested []string
	for i := range r.nets {
		n := &r.nets[i]
		if !n.Running {
			continue
		}
		for _, dom := range n.SplitDNS {
			dom = normName(dom)
			if dom == "" || !hasSuffixDomain(name, dom) {
				continue
			}
			switch {
			case len(dom) > len(bestDom):
				best, bestDom, contested = n, dom, nil
			case len(dom) == len(bestDom) && best != n:
				contested = append(contested, n.Name)
			}
		}
	}
	if best != nil {
		return Decision{Tailnet: best.Name, Kind: KindSplitDNS, Query: name, Contested: contested}
	}

	// Bare hostname: match peer short names across all tailnets.
	if !strings.Contains(name, ".") {
		var hits []Decision
		var online []bool
		for i := range r.nets {
			n := &r.nets[i]
			if !n.Running {
				continue
			}
			if p := n.peer(name); p != nil {
				d := peerDecision(n, p)
				d.Kind = KindShort
				hits = append(hits, d)
				online = append(online, p.Online)
			}
		}
		if len(hits) == 0 {
			return Decision{}
		}
		w := 0
		for i := range hits {
			if online[i] && !online[w] {
				w = i
			}
		}
		d := hits[w]
		for i, h := range hits {
			if i != w {
				d.Contested = append(d.Contested, h.Tailnet)
			}
		}
		return d
	}
	return Decision{}
}

// nameIn resolves name relative to one tailnet: a bare label or a full
// MagicDNS name maps to a peer; anything else is resolved by that
// tailnet's DNS.
func (r *Router) nameIn(n *Snapshot, name string) Decision {
	short := name
	if n.Suffix != "" {
		short = strings.TrimSuffix(name, "."+n.Suffix)
	}
	if !strings.Contains(short, ".") {
		if p := n.peer(short); p != nil {
			return peerDecision(n, p)
		}
		if n.Suffix != "" {
			return Decision{Tailnet: n.Name, Query: short + "." + n.Suffix}
		}
	}
	return Decision{Tailnet: n.Name, Query: name}
}

func (n *Snapshot) peer(short string) *Peer {
	for i := range n.Peers {
		if strings.EqualFold(n.Peers[i].Name, short) {
			return &n.Peers[i]
		}
	}
	return nil
}

func peerDecision(n *Snapshot, p *Peer) Decision {
	d := Decision{Tailnet: n.Name, Peer: p.Name, Query: p.FQDN}
	for _, ip := range p.IPs {
		d.IPs = append(d.IPs, ip.String())
	}
	return d
}

// Claims lists what the running tailnets (and pins) claim: subnet routes
// to send into a TUN, and DNS domains to point at tailmux.
func (r *Router) Claims() (prefixes []netip.Prefix, domains []string) {
	for _, n := range r.nets {
		if !n.Running {
			continue
		}
		domains = append(domains, n.Name)
		if n.Suffix != "" {
			domains = append(domains, n.Suffix)
		}
		domains = append(domains, n.SplitDNS...)
		for _, p := range n.Peers {
			prefixes = append(prefixes, p.Routes...)
		}
	}
	for p, tn := range r.pins.Prefixes {
		if r.find(tn) != nil {
			prefixes = append(prefixes, p)
		}
	}
	for d := range r.pins.Domains {
		domains = append(domains, normName(d))
	}
	for i := range prefixes {
		prefixes[i] = prefixes[i].Masked()
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int { return a.Compare(b) })
	slices.Sort(domains)
	return slices.Compact(prefixes), slices.Compact(domains)
}

// PeerAddrs lists the IPv4 address of every device in every running
// tailnet, as /32s.
func (r *Router) PeerAddrs() []netip.Prefix {
	var out []netip.Prefix
	for _, n := range r.nets {
		if !n.Running {
			continue
		}
		for _, p := range n.Peers {
			for _, ip := range p.IPs {
				if ip.Is4() {
					out = append(out, netip.PrefixFrom(ip, 32))
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return a.Compare(b) })
	return slices.Compact(out)
}
