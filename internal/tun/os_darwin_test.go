package tun

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRouteTable stands in for /sbin/route, keyed by everything after
// the add/delete command.
type fakeRouteTable struct {
	routes map[string]bool
	fail   error
}

func (f *fakeRouteTable) run(args ...string) error {
	if f.fail != nil {
		return f.fail
	}
	cmd, key := args[2], strings.Join(args[3:], " ")
	switch {
	case cmd == "add" && f.routes[key]:
		return errors.New("route: writing to routing socket: " + routeExists)
	case cmd == "delete" && !f.routes[key]:
		return errors.New("route: writing to routing socket: " + routeMissing)
	}
	f.routes[key] = cmd == "add"
	return nil
}

func (f *fakeRouteTable) has(r scopedDefault) bool {
	return f.routes[strings.Join([]string{r.family, "default", r.gateway, "-ifscope", r.ifname}, " ")]
}

func TestDarwinPin(t *testing.T) {
	table := &fakeRouteTable{routes: map[string]bool{}}
	old := runRoute
	runRoute = table.run
	oldDir := resolverDir
	resolverDir = t.TempDir()
	t.Cleanup(func() { runRoute, resolverDir = old, oldDir })

	wired := scopedDefault{family: "-inet", gateway: "192.168.50.1", ifname: "en15"}
	wifi := scopedDefault{family: "-inet", gateway: "10.244.0.1", ifname: "en0"}
	foreign := scopedDefault{family: "-inet", gateway: "10.0.0.1", ifname: "en7"}
	table.routes["-inet default 10.0.0.1 -ifscope en7"] = true
	d := &darwinOS{}

	if err := d.pin([]scopedDefault{wired, foreign}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.pinned, []scopedDefault{wired}) {
		t.Fatalf("pinned = %v; want only the route tailmux added", d.pinned)
	}

	delete(table.routes, "-inet default 192.168.50.1 -ifscope en15")
	if err := d.pin([]scopedDefault{wired}); err != nil || !table.has(wired) {
		t.Fatalf("route flushed by the OS not restored: %v", err)
	}
	if !slices.Equal(d.pinned, []scopedDefault{wired}) {
		t.Fatalf("pinned = %v; restored route lost ownership", d.pinned)
	}

	if err := d.pin([]scopedDefault{wifi}); err != nil || table.has(wired) || !table.has(wifi) {
		t.Fatalf("primary change: wired=%v wifi=%v err=%v", table.has(wired), table.has(wifi), err)
	}

	table.fail = errors.New("route: permission denied")
	if err := d.pin([]scopedDefault{wifi}); err == nil {
		t.Fatal("failed repair not reported")
	}
	if !slices.Equal(d.pinned, []scopedDefault{wifi}) {
		t.Fatalf("pinned = %v; failed repair lost ownership", d.pinned)
	}
	if err := d.pin(nil); err == nil {
		t.Fatal("failed removal not reported")
	}
	if !slices.Equal(d.pinned, []scopedDefault{wifi}) {
		t.Fatalf("pinned = %v; failed removal lost ownership", d.pinned)
	}
	if err := d.pin([]scopedDefault{{family: "-inet6", gateway: "fe80::1%en15", ifname: "en15"}}); !errors.Is(err, table.fail) || strings.Contains(err.Error(), "fe80") {
		t.Fatalf("pin = %v; want only the IPv4 removal error", err)
	}

	table.fail = nil
	if err := d.close("utun9"); err != nil || table.has(wifi) {
		t.Fatalf("close left the route behind: %v", err)
	}
	if !table.has(foreign) {
		t.Fatal("close removed another program's route")
	}
}

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
