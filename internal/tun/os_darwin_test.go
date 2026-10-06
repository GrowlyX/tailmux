package tun

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinSearchDomains(t *testing.T) {
	old := resolverDir
	resolverDir = t.TempDir()
	t.Cleanup(func() { resolverDir = old })
	d := darwinOS{}
	server := netip.MustParseAddr("198.18.0.53")
	path := filepath.Join(resolverDir, searchResolverFile)
	foreign := filepath.Join(resolverDir, "search.tailscale")
	foreignBody := "# owned by another VPN\nsearch other.ts.net\n"
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}

	set := func(domains, search []string) {
		t.Helper()
		if ok, err := d.setDNS("utun9", domains, search, server); !ok || err != nil {
			t.Fatalf("setDNS = %v, %v", ok, err)
		}
		if !d.dnsIntact("utun9", domains, search) {
			t.Fatal("DNS configuration not intact")
		}
	}
	check := func(want string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil || string(b) != resolverMarker+want {
			t.Fatalf("search file = %q, %v", b, err)
		}
	}
	set([]string{"home", "work"}, []string{"work", "home"})
	check("search work home\n")
	// Reordering search domains must not depend on alphabetically sorted claims.
	set([]string{"home", "work"}, []string{"home", "work"})
	check("search home work\n")
	if d.dnsIntact("utun9", []string{"home", "work"}, []string{"work", "home"}) {
		t.Fatal("stale search order accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if d.dnsIntact("utun9", []string{"home", "work"}, []string{"home", "work"}) {
		t.Fatal("missing search file accepted")
	}
	set([]string{"home"}, []string{"home"})
	check("search home\n")
	if _, err := os.Stat(filepath.Join(resolverDir, "work")); !os.IsNotExist(err) {
		t.Fatal("disabled tailnet resolver left behind")
	}
	set(nil, nil)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty search list left behind")
	}
	set([]string{"work"}, []string{"work"})
	if err := d.close("utun9"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("search file left behind on close")
	}
	set([]string{"work"}, []string{"work"})
	if _, err := newOSConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("search file left behind after crash cleanup")
	}
	b, err := os.ReadFile(foreign)
	if err != nil || string(b) != foreignBody {
		t.Fatal("another VPN's resolver changed")
	}
	// Never overwrite or delete a foreign file, even at our reserved filename.
	if err := os.WriteFile(path, []byte(foreignBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.setDNS("utun9", []string{"work"}, []string{"work"}, server); ok || err == nil {
		t.Fatal("foreign search file accepted")
	}
	d.close("utun9")
	b, err = os.ReadFile(path)
	if err != nil || string(b) != foreignBody {
		t.Fatal("foreign search file changed")
	}
	for _, search := range []string{"", "work\nsearch attacker", "../work", "work home"} {
		if _, err := d.setDNS("utun9", nil, []string{search}, server); err == nil {
			t.Fatalf("accepted unsafe search suffix %q", search)
		}
	}
}

func TestParseRouteGet(t *testing.T) {
	out := `   route to: default
destination: default
       mask: default
    gateway: 192.168.50.1
  interface: en15
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`
	got, ok := parseRouteGet("-inet", out)
	want := scopedDefault{family: "-inet", gateway: "192.168.50.1", ifname: "en15"}
	if !ok || got != want {
		t.Fatalf("parseRouteGet = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := parseRouteGet("-inet", "   route to: default\n  interface: ppp0\n"); ok {
		t.Fatal("route without a gateway accepted")
	}
}
