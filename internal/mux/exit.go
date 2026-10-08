package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// Exit nodes: with one selected, everything no tailnet claims leaves
// through it instead of going direct, the way the official client's exit
// node menu works. Destinations on the local network stay direct. While
// the exit node can't be used (its tailnet is down, the device is gone)
// that traffic fails rather than quietly going direct.

const KindExit Kind = "exit-node"

// ExitNode is the configured exit node, or nil.
func (m *Mux) ExitNode() *ExitNodeConfig {
	return m.exitCfg.Load()
}

// SetExitNode switches the exit node now and saves the choice. An empty
// tailnet turns it off.
func (m *Mux) SetExitNode(ctx context.Context, tailnet, node string) (*ExitStatus, error) {
	tailnet = strings.ToLower(strings.TrimSpace(tailnet))
	node = strings.TrimSpace(node)
	var want *ExitNodeConfig
	if tailnet != "" || node != "" {
		t := m.get(tailnet)
		if t == nil {
			return nil, fmt.Errorf("no tailnet named %q", tailnet)
		}
		p := findPeer(t.Snapshot().Peers, node)
		switch {
		case node == "":
			return nil, errors.New("which exit node? (name, IP or ID)")
		case p == nil:
			return nil, fmt.Errorf("no device %q in tailnet %s", node, tailnet)
		case !p.Exit:
			return nil, fmt.Errorf("%s does not offer itself as an exit node", p.Name)
		}
		// Save the MagicDNS name: readable, and stable for Mullvad nodes.
		want = &ExitNodeConfig{Tailnet: tailnet, Node: cmpOr(p.FQDN, node)}
	}
	prev := m.exitCfg.Swap(want)
	for _, t := range m.list() {
		spec := ""
		if want != nil && t.cfg.Name == want.Tailnet {
			spec = want.Node
		}
		t.setExitNode(ctx, spec)
	}
	// Only once the tailnets use the new node: connections dialed while
	// they still used the old one must go too.
	if !sameExitNode(prev, want) {
		m.exitConns.closeAll()
	}
	m.rebuild()
	if err := m.editConfig(func(c *Config) { c.ExitNode = want }); err != nil {
		return m.exitStatus(), fmt.Errorf("switched, but saving the config failed: %w", err)
	}
	return m.exitStatus(), nil
}

// exitTailnet is the tailnet to send non-tailnet traffic through, or nil
// for direct. An exit node that's configured but unusable is an error.
func (m *Mux) exitTailnet() (*Tailnet, error) {
	e := m.ExitNode()
	if e == nil {
		return nil, nil
	}
	t := m.get(e.Tailnet)
	if t == nil {
		return nil, fmt.Errorf("exit node %s: tailnet %s was removed", e.Node, e.Tailnet)
	}
	if ok, why := t.exitReady(); !ok {
		return nil, fmt.Errorf("exit node %s: %s", e.Node, why)
	}
	return t, nil
}

func sameExitNode(a, b *ExitNodeConfig) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}

// exitConns are the connections open through the exit node. They're
// closed when it changes, so apps reconnect over the new path right
// away instead of waiting on a connection that no longer goes anywhere.
type exitConns struct {
	mu    sync.Mutex
	conns map[*exitConn]struct{}
}

// trackExit adds c, dialed through via, or closes it if the exit node
// changed while it was being dialed.
func (m *Mux) trackExit(c net.Conn, via *ExitNodeConfig) (net.Conn, error) {
	s := &m.exitConns
	s.mu.Lock()
	defer s.mu.Unlock()
	if !sameExitNode(m.exitCfg.Load(), via) {
		c.Close()
		return nil, errors.New("exit node changed")
	}
	ec := &exitConn{Conn: c, set: s}
	if s.conns == nil {
		s.conns = map[*exitConn]struct{}{}
	}
	s.conns[ec] = struct{}{}
	return ec, nil
}

func (s *exitConns) closeAll() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for c := range conns {
		c.Conn.Close()
	}
}

type exitConn struct {
	net.Conn
	set *exitConns
}

func (c *exitConn) Close() error {
	c.set.mu.Lock()
	delete(c.set.conns, c)
	c.set.mu.Unlock()
	return c.Conn.Close()
}

func (c *exitConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// localName: names that only mean something on the local network, which
// an exit node's resolver can't answer.
func localName(host string) bool {
	h := normName(host)
	return !strings.Contains(h, ".") || hasSuffixDomain(h, "local") || hasSuffixDomain(h, "home.arpa")
}

// ExitStatus is the exit node part of /status.
type ExitStatus struct {
	Tailnet  string    `json:"tailnet"`
	Node     string    `json:"node"`
	Name     string    `json:"name,omitempty"`
	FQDN     string    `json:"fqdn,omitempty"`
	Online   bool      `json:"online"`
	Active   bool      `json:"active"`
	Location *Location `json:"location,omitempty"`
	Error    string    `json:"error,omitempty"`
}

func (m *Mux) exitStatus() *ExitStatus {
	e := m.ExitNode()
	if e == nil {
		return nil
	}
	s := &ExitStatus{Tailnet: e.Tailnet, Node: e.Node}
	t := m.get(e.Tailnet)
	if t == nil {
		s.Error = "tailnet " + e.Tailnet + " was removed"
		return s
	}
	if p := findPeer(t.Snapshot().Peers, e.Node); p != nil {
		s.Name, s.FQDN, s.Online = p.Name, p.FQDN, p.Online
		if p.Location != (Location{}) {
			l := p.Location
			s.Location = &l
		}
	}
	s.Active, s.Error = t.exitReady()
	return s
}

// ExitNodeInfo is one entry of GET /exit-nodes.
type ExitNodeInfo struct {
	Tailnet  string    `json:"tailnet"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	FQDN     string    `json:"fqdn"`
	IPs      []string  `json:"ips,omitempty"`
	Online   bool      `json:"online"`
	Mullvad  bool      `json:"mullvad,omitempty"`
	Location *Location `json:"location,omitempty"`
	Selected bool      `json:"selected,omitempty"`
}

// ExitNodes lists every device offering itself as an exit node, in every
// running tailnet: your own first, then located ones (Mullvad) by
// country and city.
func (m *Mux) ExitNodes() []ExitNodeInfo {
	cur := m.ExitNode()
	var out []ExitNodeInfo
	for _, t := range m.list() {
		snap := t.Snapshot()
		if !snap.active() {
			continue
		}
		var sel *Peer
		if cur != nil && cur.Tailnet == snap.Name {
			sel = findPeer(snap.Peers, cur.Node)
		}
		for i := range snap.Peers {
			p := &snap.Peers[i]
			if !p.Exit {
				continue
			}
			e := ExitNodeInfo{Tailnet: snap.Name, ID: p.ID, Name: p.Name, FQDN: p.FQDN, Online: p.Online,
				Mullvad: p.Mullvad(), Selected: p == sel}
			for _, ip := range p.IPs {
				e.IPs = append(e.IPs, ip.String())
			}
			if p.Location != (Location{}) {
				l := p.Location
				e.Location = &l
			}
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b ExitNodeInfo) int {
		la, lb := cmpOr(a.Location, &Location{}), cmpOr(b.Location, &Location{})
		switch {
		case (a.Location == nil) != (b.Location == nil):
			if a.Location == nil {
				return -1
			}
			return 1
		case la.Country != lb.Country:
			return strings.Compare(la.Country, lb.Country)
		case la.City != lb.City:
			return strings.Compare(la.City, lb.City)
		case la.Priority != lb.Priority:
			return lb.Priority - la.Priority
		}
		return strings.Compare(a.FQDN, b.FQDN)
	})
	return out
}

func cmpOr[T comparable](a, b T) T {
	var zero T
	if a != zero {
		return a
	}
	return b
}

// --- local network ---

// localNets caches the local interfaces' prefixes for isLocal.
type localNets struct {
	mu   sync.Mutex
	at   time.Time
	nets []netip.Prefix
}

func (l *localNets) contains(ip netip.Addr) bool {
	l.mu.Lock()
	if time.Since(l.at) > 5*time.Second {
		l.nets, l.at = LocalPrefixes(""), time.Now()
	}
	nets := l.nets
	l.mu.Unlock()
	return slices.ContainsFunc(nets, func(p netip.Prefix) bool { return p.Contains(ip) })
}

// isLocal: destinations that never go through an exit node.
func (m *Mux) isLocal(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || m.localNets.contains(ip)
}

// LocalPrefixes lists the networks of the local interfaces, other than
// skip, loopback and point-to-point links (VPNs).
func LocalPrefixes(skip string) []netip.Prefix {
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

// --- HTTP ---

func (m *Mux) serveExitNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"current": m.exitStatus(), "nodes": m.ExitNodes()})
}

func (m *Mux) serveSetExitNode(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var e ExitNodeConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&e); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s, err := m.SetExitNode(ctx, e.Tailnet, e.Node)
	if err != nil && s == nil && (e.Tailnet != "" || e.Node != "") {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"current": s})
}
