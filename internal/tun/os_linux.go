package tun

import (
	"errors"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const defaultTUNName = "tailmux0"

type linuxOS struct{}

func newOSConfig() (osConfig, error) { return linuxOS{}, nil }

func (l linuxOS) up(ifname string, gw netip.Addr, fake netip.Prefix, mtu int) error {
	l.clearExit() // left over from a crash
	if err := run("ip", "link", "set", "dev", ifname, "mtu", itoa(mtu), "up"); err != nil {
		return err
	}
	if err := run("ip", "addr", "replace", gw.String()+"/32", "dev", ifname); err != nil {
		return err
	}
	return linuxOS{}.addRoute(ifname, fake)
}

func ipCmd(p netip.Prefix, args ...string) []string {
	if p.Addr().Is6() {
		return append([]string{"-6"}, args...)
	}
	return args
}

func (linuxOS) addRoute(ifname string, p netip.Prefix) error {
	return run("ip", ipCmd(p, "route", "replace", p.String(), "dev", ifname)...)
}

func (linuxOS) delRoute(ifname string, p netip.Prefix) error {
	return run("ip", ipCmd(p, "route", "del", p.String(), "dev", ifname)...)
}

// Exit node routing on Linux is policy routing, like tailscaled's:
// tailscale's netns marks tsnet's sockets (SO_MARK 0x80000), and only
// unmarked traffic is sent to our table, whose default route is the TUN.
// Everything in the main table except its default route (the LAN,
// tailnet subnets on the TUN, docker bridges) is looked up first. The
// rule numbers come after tailscaled's 5210-5270, so both can coexist.
const (
	exitTable      = "5280"
	prefMainNoDflt = "5280"
	prefExit       = "5290"
	bypassMark     = "0x80000/0xff0000"
)

func (l linuxOS) setExit(ifname string, on bool) error {
	if !on {
		l.clearExit()
		return nil
	}
	if l.exitRulesPresent() {
		// Re-applying (after a network change): replace the route in place
		// rather than dropping the rules, which would let traffic out
		// directly for a moment.
		run("ip", "-6", "route", "replace", "default", "dev", ifname, "table", exitTable)
		return run("ip", "-4", "route", "replace", "default", "dev", ifname, "table", exitTable)
	}
	l.clearExit()
	var errs []error
	for _, fam := range []string{"-4", "-6"} {
		err := errors.Join(
			run("ip", fam, "route", "replace", "default", "dev", ifname, "table", exitTable),
			run("ip", fam, "rule", "add", "pref", prefMainNoDflt, "lookup", "main", "suppress_prefixlength", "0"),
			run("ip", fam, "rule", "add", "pref", prefExit, "not", "fwmark", bypassMark, "lookup", exitTable),
		)
		if err != nil && fam == "-6" {
			log.Printf("tun: exit node: no IPv6 routing: %v", err)
			continue
		}
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		l.clearExit()
		return err
	}
	if b, _ := os.ReadFile("/proc/sys/net/ipv4/conf/all/rp_filter"); strings.TrimSpace(string(b)) == "1" {
		log.Printf("tun: exit node: strict reverse path filtering (net.ipv4.conf.all.rp_filter=1) can drop replies; set it to 2")
	}
	return nil
}

// exitRulesPresent reports whether both IPv4 rules are in place.
func (linuxOS) exitRulesPresent() bool {
	for _, pref := range []string{prefMainNoDflt, prefExit} {
		out, err := exec.Command("ip", "-4", "rule", "show", "pref", pref).Output()
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			return false
		}
	}
	return true
}

func (linuxOS) clearExit() {
	for _, fam := range []string{"-4", "-6"} {
		for _, pref := range []string{prefMainNoDflt, prefExit} {
			for i := 0; i < 8 && exec.Command("ip", fam, "rule", "del", "pref", pref).Run() == nil; i++ {
			}
		}
		exec.Command("ip", fam, "route", "flush", "table", exitTable).Run()
	}
}

// setDNS uses systemd-resolved's per-link routing domains ("~dom"), so
// only tailnet names come here (every name, "~.", with an exit node). Without resolved there is no standard
// per-domain mechanism, and tailmux leaves /etc/resolv.conf alone.
func (linuxOS) setDNS(ifname string, domains, _ []string, server netip.Addr) (bool, error) {
	if !resolvedRunning() {
		return false, nil
	}
	if err := run("resolvectl", "dns", ifname, server.String()); err != nil {
		return false, err
	}
	args := []string{"domain", ifname}
	all := false
	for _, d := range domains {
		args = append(args, "~"+d)
		all = all || d == "."
	}
	if len(domains) == 0 {
		args = append(args, "")
	}
	if err := run("resolvectl", args...); err != nil {
		return false, err
	}
	// "~." makes this link the resolver for every name (exit node).
	run("resolvectl", "default-route", ifname, strconv.FormatBool(all))
	return true, nil
}

func (linuxOS) dnsIntact(ifname string, domains, _ []string) bool {
	if !resolvedRunning() || len(domains) == 0 {
		return true
	}
	out, err := exec.Command("resolvectl", "domain", ifname).CombinedOutput()
	return err == nil && strings.Contains(string(out), "~"+domains[0])
}

func (linuxOS) flushDNS() {
	if resolvedRunning() {
		exec.Command("resolvectl", "flush-caches").Run()
	}
}

func resolvedRunning() bool {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return false
	}
	out, err := exec.Command("resolvectl", "status").CombinedOutput()
	return err == nil && !strings.Contains(string(out), "Failed")
}

// The link and everything attached to it (routes, resolved settings) go
// away when the TUN closes; the exit node rules don't.
func (l linuxOS) close(ifname string) error {
	l.clearExit()
	if resolvedRunning() {
		exec.Command("resolvectl", "revert", ifname).Run()
	}
	return nil
}
