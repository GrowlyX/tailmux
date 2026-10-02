package tun

import (
	"net/netip"
	"os/exec"
	"strings"
)

const defaultTUNName = "tailmux0"

type linuxOS struct{}

func newOSConfig() (osConfig, error) { return linuxOS{}, nil }

func (linuxOS) up(ifname string, gw netip.Addr, fake netip.Prefix, mtu int) error {
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

// setDNS uses systemd-resolved's per-link routing domains ("~dom"), so
// only tailnet names come here. Without resolved there is no standard
// per-domain mechanism, and tailmux leaves /etc/resolv.conf alone.
func (linuxOS) setDNS(ifname string, domains, _ []string, server netip.Addr) (bool, error) {
	if !resolvedRunning() {
		return false, nil
	}
	if err := run("resolvectl", "dns", ifname, server.String()); err != nil {
		return false, err
	}
	args := []string{"domain", ifname}
	for _, d := range domains {
		args = append(args, "~"+d)
	}
	if len(domains) == 0 {
		args = append(args, "")
	}
	if err := run("resolvectl", args...); err != nil {
		return false, err
	}
	run("resolvectl", "default-route", ifname, "false")
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
// away when the TUN closes.
func (linuxOS) close(ifname string) error {
	if resolvedRunning() {
		exec.Command("resolvectl", "revert", ifname).Run()
	}
	return nil
}
