package mux

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"testing"
)

func ip(s string) netip.Addr    { return netip.MustParseAddr(s) }
func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func ips(s ...string) []netip.Addr {
	var o []netip.Addr
	for _, v := range s {
		o = append(o, ip(v))
	}
	return o
}

func testRouter(pins Pins) *Router {
	return NewRouter([]Snapshot{
		{
			Name: "work", Priority: 0, Running: true, Suffix: "tailaaaa.ts.net",
			SplitDNS: []string{"corp.internal"},
			Peers: []Peer{
				{Name: "db", FQDN: "db.tailaaaa.ts.net", IPs: ips("100.64.0.1"), Online: true},
				{Name: "gw", FQDN: "gw.tailaaaa.ts.net", IPs: ips("100.64.0.2"), Online: true, Routes: []netip.Prefix{pfx("10.0.0.0/16")}},
			},
		},
		{
			Name: "home", Priority: 1, Running: true, Suffix: "tailbbbb.ts.net",
			Peers: []Peer{
				{Name: "db", FQDN: "db.tailbbbb.ts.net", IPs: ips("100.64.0.1"), Online: true},
				{Name: "nas", FQDN: "nas.tailbbbb.ts.net", IPs: ips("100.64.0.9"), Online: true, Routes: []netip.Prefix{pfx("10.0.5.0/24"), pfx("192.168.1.0/24")}},
			},
		},
		{
			Name: "lab", Priority: 2, Running: true, Suffix: "tailcccc.ts.net",
			Peers: []Peer{
				{Name: "mini", FQDN: "mini.tailcccc.ts.net", IPs: ips("100.70.0.3"), Online: false, Routes: []netip.Prefix{pfx("192.168.1.0/24")}},
			},
		},
	}, pins)
}

func TestRouteIP(t *testing.T) {
	r := testRouter(Pins{})
	cases := []struct {
		ip, want string
		kind     Kind
		contest  []string
	}{
		{"100.64.0.2", "work", KindPeerIP, nil},
		{"100.64.0.9", "home", KindPeerIP, nil},
		{"100.64.0.1", "work", KindPeerIP, []string{"home"}}, // same IP in two tailnets: priority
		{"10.0.1.1", "work", KindSubnet, nil},
		{"10.0.5.7", "home", KindSubnet, nil},                 // /24 beats /16
		{"192.168.1.10", "home", KindSubnet, []string{"lab"}}, // tie on /24: online router wins
		{"8.8.8.8", "", "", nil},
	}
	for _, c := range cases {
		d := r.RouteIP(ip(c.ip))
		if d.Tailnet != c.want || d.Kind != c.kind || !slices.Equal(d.Contested, c.contest) {
			t.Errorf("RouteIP(%s) = %+v, want %s/%s contested=%v", c.ip, d, c.want, c.kind, c.contest)
		}
	}
}

func TestRouteName(t *testing.T) {
	r := testRouter(Pins{})
	cases := []struct {
		host, want string
		kind       Kind
		query      string
	}{
		{"db.tailbbbb.ts.net", "home", KindMagicDNS, "db.tailbbbb.ts.net"},
		{"DB.TailAAAA.ts.net.", "work", KindMagicDNS, "db.tailaaaa.ts.net"},
		{"db.home", "home", KindAlias, "db.tailbbbb.ts.net"},
		{"db", "work", KindShort, "db.tailaaaa.ts.net"}, // ambiguous: priority
		{"nas", "home", KindShort, "nas.tailbbbb.ts.net"},
		{"svc.tailcccc.ts.net", "lab", KindMagicDNS, "svc.tailcccc.ts.net"}, // unknown peer: ask lab's DNS
		{"grafana.corp.internal", "work", KindSplitDNS, "grafana.corp.internal"},
		{"10.0.5.1", "home", KindSubnet, ""},
		{"example.com", "", "", ""},
		{"nosuchhost", "", "", ""},
	}
	for _, c := range cases {
		d := r.RouteName(c.host)
		if d.Tailnet != c.want || d.Kind != c.kind || d.Query != c.query {
			t.Errorf("RouteName(%q) = %+v, want %s/%s query=%q", c.host, d, c.want, c.kind, c.query)
		}
	}
}

func TestPins(t *testing.T) {
	r := testRouter(Pins{
		Prefixes: map[netip.Prefix]string{pfx("192.168.1.0/24"): "lab", pfx("100.64.0.1/32"): "home"},
		Domains:  map[string]string{"db": "home"},
	})
	if d := r.RouteIP(ip("192.168.1.10")); d.Tailnet != "lab" || d.Kind != KindPinned {
		t.Errorf("pinned subnet: %+v", d)
	}
	if d := r.RouteIP(ip("100.64.0.1")); d.Tailnet != "home" {
		t.Errorf("pinned ip: %+v", d)
	}
	if d := r.RouteName("db"); d.Tailnet != "home" || d.Query != "db.tailbbbb.ts.net" {
		t.Errorf("pinned name: %+v", d)
	}
}

func TestStoppedTailnetIgnored(t *testing.T) {
	snaps := testRouter(Pins{}).Snapshots()
	snaps[1].Running = false // home down
	r := NewRouter(snaps, Pins{})
	if d := r.RouteIP(ip("192.168.1.10")); d.Tailnet != "lab" {
		t.Errorf("want failover to lab, got %+v", d)
	}
}

func TestConflicts(t *testing.T) {
	cs := testRouter(Pins{}).Conflicts()
	var got []string
	for _, c := range cs {
		got = append(got, c.What+"="+c.Winner)
	}
	want := []string{"192.168.1.0/24=home", "db=work"}
	if !slices.Equal(got, want) {
		t.Errorf("conflicts = %v, want %v", got, want)
	}
}

func TestUsefulSplitDomain(t *testing.T) {
	for dom, want := range map[string]bool{
		"corp.internal":       true,
		"ts.net":              false, // every tailnet has it
		"tail1234.ts.net":     false, // our own MagicDNS suffix
		"1234.ts.net":         true,  // shares characters, not labels
		"64.100.in-addr.arpa": false,
		"":                    false,
		"other.ts.net":        true, // not a parent of our suffix: someone's real split domain
	} {
		if got := usefulSplitDomain(dom, "tail1234.ts.net"); got != want {
			t.Errorf("usefulSplitDomain(%q) = %v, want %v", dom, got, want)
		}
	}
}

// A self-hosted control server often has a MagicDNS suffix under a domain
// it also serves as split DNS (ts.example.com under example.com).
func TestUsefulSplitDomainParentOfSuffix(t *testing.T) {
	for dom, want := range map[string]bool{
		"example.com":    true,  // parent of the suffix, but a real split domain
		"ts.example.com": false, // the suffix itself
		"ts.net":         false, // still every tailnet's
	} {
		if got := usefulSplitDomain(dom, "ts.example.com"); got != want {
			t.Errorf("usefulSplitDomain(%q) = %v, want %v", dom, got, want)
		}
	}
}

func TestPeerAddrs(t *testing.T) {
	got := testRouter(Pins{}).PeerAddrs()
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	// 100.64.0.1 is in two tailnets but routed once.
	want := []string{"100.64.0.1/32", "100.64.0.2/32", "100.64.0.9/32", "100.70.0.3/32"}
	if !slices.Equal(s, want) {
		t.Errorf("got %v, want %v", s, want)
	}
}

func TestReconnectingKeepsClaims(t *testing.T) {
	snaps := testRouter(Pins{}).Snapshots()
	snaps[1].Running, snaps[1].Reconnecting = false, true // home, just woke up
	r := NewRouter(snaps, Pins{})
	if d := r.RouteName("nas.home"); d.Tailnet != "home" {
		t.Errorf("reconnecting tailnet lost its names: %+v", d)
	}
	if d := r.RouteIP(ip("10.0.5.7")); d.Tailnet != "home" {
		t.Errorf("reconnecting tailnet lost its routes: %+v", d)
	}
	_, doms := r.Claims()
	if !slices.Contains(doms, "tailbbbb.ts.net") {
		t.Errorf("reconnecting tailnet's DNS domain withdrawn: %v", doms)
	}
	snaps[1].Reconnecting = false // logged out / switched off
	stopped := NewRouter(snaps, Pins{})
	if d := stopped.RouteIP(ip("10.0.5.7")); d.Tailnet == "home" {
		t.Errorf("stopped tailnet still routes: %+v", d)
	}
	if _, doms := stopped.Claims(); slices.Contains(doms, "tailbbbb.ts.net") {
		t.Errorf("stopped tailnet still claims its DNS domain: %v", doms)
	}
}

func TestIsNotFound(t *testing.T) {
	for err, want := range map[error]bool{
		fmt.Errorf("x: %w in tailnet y", ErrNoSuchHost):      true,
		&net.DNSError{Err: "no such host", IsNotFound: true}: true,
		&net.DNSError{Err: "i/o timeout", IsTimeout: true}:   false,
		errors.New("context deadline exceeded"):              false,
	} {
		if got := IsNotFound(err); got != want {
			t.Errorf("IsNotFound(%v) = %v", err, got)
		}
	}
}

func TestSearchDomains(t *testing.T) {
	r := NewRouter([]Snapshot{
		{Name: "home", Priority: 2, Reconnecting: true},
		{Name: "disabled", Priority: 0},
		{Name: "work", Priority: 1, Running: true, SplitDNS: []string{"corp.internal"}},
	}, Pins{Domains: map[string]string{"pinned.example": "work"}})
	if got := r.SearchDomains(); !slices.Equal(got, []string{"work", "home"}) {
		t.Fatalf("SearchDomains = %v", got)
	}
	r.nets[1].Running = false
	if got := r.SearchDomains(); !slices.Equal(got, []string{"home"}) {
		t.Fatalf("SearchDomains after stop = %v", got)
	}
}

func TestPeerMullvad(t *testing.T) {
	for fqdn, want := range map[string]bool{
		"us-nyc-wg-301.mullvad.ts.net": true,
		"nas.tailbbbb.ts.net":          false,
		"mullvad.tailbbbb.ts.net":      false,
	} {
		if got := (Peer{FQDN: fqdn}).Mullvad(); got != want {
			t.Errorf("%s: Mullvad() = %v, want %v", fqdn, got, want)
		}
	}
}
