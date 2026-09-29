package tun

import (
	"bytes"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultTUNName = "utun"

// resolverDir holds macOS per-domain resolver files (man 5 resolver).
// mDNSResponder picks up changes on its own.
var resolverDir = "/etc/resolver"

const resolverMarker = "# managed by tailmux; removed when it stops\n"

type darwinOS struct{}

func newOSConfig() (osConfig, error) {
	d := darwinOS{}
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
	if err != nil && strings.Contains(err.Error(), "File exists") {
		// Already there: either ours (re-applying) or the official
		// client's for the same subnet. Point it at us.
		return run("/sbin/route", "-q", "-n", "change", family(p), "-net", p.String(), "-interface", ifname)
	}
	return err
}

func (darwinOS) delRoute(ifname string, p netip.Prefix) error {
	return run("/sbin/route", "-q", "-n", "delete", family(p), "-net", p.String(), "-interface", ifname)
}

func (d darwinOS) setDNS(ifname string, domains []string, server netip.Addr) (bool, error) {
	if err := os.MkdirAll(resolverDir, 0o755); err != nil {
		return false, err
	}
	want := map[string]bool{}
	body := []byte(resolverMarker + "nameserver " + server.String() + "\n")
	for _, dom := range domains {
		if dom == "" || strings.ContainsAny(dom, "/\\") {
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

func (darwinOS) dnsIntact(_ string, domains []string) bool {
	for _, dom := range domains {
		b, err := os.ReadFile(filepath.Join(resolverDir, dom))
		if err != nil {
			return false
		}
		if !bytes.HasPrefix(b, []byte(resolverMarker)) {
			continue // someone else's file; setDNS leaves those alone too
		}
	}
	return true
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

// Routes on a utun disappear with it; only the resolver files persist.
func (d darwinOS) close(string) error {
	d.removeResolvers()
	return nil
}
