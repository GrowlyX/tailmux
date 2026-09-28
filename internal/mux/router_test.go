package mux

import (
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
