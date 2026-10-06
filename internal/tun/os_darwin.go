package tun

import (
	"bytes"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"tailscale.com/util/dnsname"
)

const defaultTUNName = "utun"

// resolverDir holds macOS per-domain resolver files (man 5 resolver).
// mDNSResponder picks up changes on its own.
var resolverDir = "/etc/resolver"

const searchResolverFile = "search.tailmux"

const resolverMarker = "# managed by tailmux; removed when it stops\n"

const routeExists = "File exists"

type darwinOS struct {
	// pinned are the scoped default routes setExit added.
	pinned []scopedDefault
}

// scopedDefault is a default route that only sockets bound to ifname use.
type scopedDefault struct {
	family, gateway, ifname string
}

func newOSConfig() (osConfig, error) {
	d := &darwinOS{}
	d.removeResolvers() // leftovers from a crash
	return d, nil
}

func (darwinOS) up(ifname string, gw netip.Addr, fake netip.Prefix, mtu int) error {
	if err := run("/sbin/ifconfig", ifname, "inet", gw.String(), gw.String(), "netmask", "255.255.255.255", "mtu", itoa(mtu), "up"); err != nil {
		return err
	}
	return darwinOS{}.addRoute(ifname, fake)
}

func family(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "-inet"
	}
	return "-inet6"
}

func (darwinOS) addRoute(ifname string, p netip.Prefix) error {
	err := run("/sbin/route", "-q", "-n", "add", family(p), "-net", p.String(), "-interface", ifname)
	if err != nil && strings.Contains(err.Error(), routeExists) {
		// Already there: either ours (re-applying) or the official
		// client's for the same subnet. Point it at us.
		return run("/sbin/route", "-q", "-n", "change", family(p), "-net", p.String(), "-interface", ifname)
	}
	return err
}

func (darwinOS) delRoute(ifname string, p netip.Prefix) error {
	return run("/sbin/route", "-q", "-n", "delete", family(p), "-net", p.String(), "-interface", ifname)
}

// setExit relies on tailscale's netns binding tsnet's sockets to the
// default route's interface (IP_BOUND_IF). Bound sockets ignore the
// halves only if that interface has its own scoped default route, and
// macOS gives one to every interface except the primary.
func (d *darwinOS) setExit(ifname string, on bool) error {
	var want []scopedDefault
	if on {
		want = primaryDefaults()
	}
	d.pin(want)
	return setExitRoutes(on,
		func(p netip.Prefix) error { return d.addRoute(ifname, p) },
		func(p netip.Prefix) error { return d.delRoute(ifname, p) })
}

func primaryDefaults() []scopedDefault {
	var out []scopedDefault
	for _, family := range []string{"-inet", "-inet6"} {
		b, err := exec.Command("/sbin/route", "-n", "get", family, "default").Output()
		if err != nil {
			continue
		}
		if r, ok := parseRouteGet(family, string(b)); ok {
			out = append(out, r)
		}
	}
	return out
}

// parseRouteGet reads the output of `route -n get default`.
func parseRouteGet(family, out string) (scopedDefault, bool) {
	r := scopedDefault{family: family}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch key {
		case "gateway":
			r.gateway = strings.TrimSpace(val)
		case "interface":
			r.ifname = strings.TrimSpace(val)
		}
	}
	return r, r.gateway != "" && r.ifname != ""
}

// pin adds the scoped default routes in want and removes the ones it
// added before that aren't. Routes that were already there aren't ours.
func (d *darwinOS) pin(want []scopedDefault) {
	var pinned []scopedDefault
	for _, r := range d.pinned {
		if slices.Contains(want, r) {
			pinned = append(pinned, r)
		} else if err := r.route("delete"); err != nil {
			log.Printf("tun: remove scoped default route: %v", err)
		}
	}
	for _, r := range want {
		if slices.Contains(pinned, r) {
			continue
		}
		switch err := r.route("add"); {
		case err == nil:
			pinned = append(pinned, r)
		case !strings.Contains(err.Error(), routeExists):
			log.Printf("tun: add scoped default route: %v", err)
		}
	}
	d.pinned = pinned
}

func (r scopedDefault) route(cmd string) error {
	return run("/sbin/route", "-q", "-n", cmd, r.family, "default", r.gateway, "-ifscope", r.ifname)
}

func (d darwinOS) setDNS(ifname string, domains, searchDomains []string, server netip.Addr) (bool, error) {
	if err := os.MkdirAll(resolverDir, 0o755); err != nil {
		return false, err
	}
	searchBody, err := searchResolverBody(searchDomains)
	if err != nil {
		return false, err
	}
	for _, dom := range domains {
		if dom == searchResolverFile {
			return false, fmt.Errorf("DNS domain %q is reserved for search domains", dom)
		}
	}
	want := map[string]bool{}
	if len(searchDomains) > 0 {
		path := filepath.Join(resolverDir, searchResolverFile)
		cur, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if err == nil && !bytes.HasPrefix(cur, []byte(resolverMarker)) {
			return false, fmt.Errorf("DNS search resolver %s is not owned by tailmux", path)
		}
		if err := os.WriteFile(path, searchBody, 0o644); err != nil {
			return false, err
		}
		want[searchResolverFile] = true
	}
	body := []byte(resolverMarker + "nameserver " + server.String() + "\n")
	for _, dom := range domains {
		// "." (every name, for an exit node) has no /etc/resolver form;
		// macOS keeps using the network's resolver for other names.
		if dom == "" || dom == "." || strings.ContainsAny(dom, "/\\") {
			continue
		}
		want[dom] = true
		path := filepath.Join(resolverDir, dom)
		if cur, err := os.ReadFile(path); err == nil && !bytes.HasPrefix(cur, []byte(resolverMarker)) {
			continue // someone else's file; leave it alone
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return false, err
		}
	}
	d.removeResolvers(want)
	return true, nil
}

func (darwinOS) dnsIntact(_ string, domains, searchDomains []string) bool {
	for _, dom := range domains {
		if dom == "." {
			continue
		}
		b, err := os.ReadFile(filepath.Join(resolverDir, dom))
		if err != nil {
			return false
		}
		if !bytes.HasPrefix(b, []byte(resolverMarker)) {
			continue // someone else's file; setDNS leaves those alone too
		}
	}
	body, err := searchResolverBody(searchDomains)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(resolverDir, searchResolverFile))
	if len(searchDomains) > 0 {
		return err == nil && bytes.Equal(b, body)
	}
	return os.IsNotExist(err) || (err == nil && !bytes.HasPrefix(b, []byte(resolverMarker)))
}

func searchResolverBody(domains []string) ([]byte, error) {
	for _, dom := range domains {
		if err := dnsname.ValidLabel(dom); err != nil {
			return nil, fmt.Errorf("invalid DNS search domain %q: %w", dom, err)
		}
	}
	return []byte(resolverMarker + "search " + strings.Join(domains, " ") + "\n"), nil
}

// flushDNS clears the system resolver cache, including failures cached
// while tailnets were still reconnecting.
func (darwinOS) flushDNS() {
	exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
	exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
}

// removeResolvers deletes tailmux's resolver files except those in keep.
func (darwinOS) removeResolvers(keep ...map[string]bool) {
	ents, _ := os.ReadDir(resolverDir)
	for _, e := range ents {
		if len(keep) > 0 && keep[0][e.Name()] {
			continue
		}
		path := filepath.Join(resolverDir, e.Name())
		if b, err := os.ReadFile(path); err == nil && bytes.HasPrefix(b, []byte(resolverMarker)) {
			os.Remove(path)
		}
	}
}

// Routes on a utun disappear with it; the scoped default routes and
// resolver files don't.
func (d *darwinOS) close(string) error {
	d.pin(nil)
	d.removeResolvers()
	return nil
}
