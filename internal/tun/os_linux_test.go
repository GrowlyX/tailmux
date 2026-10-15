package tun

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const netnsEnv = "TAILMUX_TEST_NETNS"

// TestBypassRules runs the real ip commands in a throwaway network
// namespace: it re-executes itself under unshare -Urn.
func TestBypassRules(t *testing.T) {
	if os.Getenv(netnsEnv) == "" {
		if _, err := exec.LookPath("ip"); err != nil {
			t.Skip("no ip command")
		}
		if err := exec.Command("unshare", "-Urn", "true").Run(); err != nil {
			t.Skipf("no unprivileged network namespaces: %v", err)
		}
		cmd := exec.Command("unshare", "-Urn", os.Args[0], "-test.run=^TestBypassRules$", "-test.v")
		cmd.Env = append(os.Environ(), netnsEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}

	sh := func(args ...string) {
		t.Helper()
		if err := run("ip", args...); err != nil {
			t.Fatal(err)
		}
	}
	sh("link", "add", "wan0", "type", "dummy")
	sh("link", "set", "wan0", "up")
	sh("addr", "add", "203.0.113.10/24", "dev", "wan0")
	sh("route", "add", "default", "via", "203.0.113.1", "dev", "wan0")
	sh("-6", "addr", "add", "2001:db8:1::10/64", "dev", "wan0", "nodad")
	sh("-6", "route", "add", "default", "via", "2001:db8:1::1", "dev", "wan0")
	sh("link", "add", "tm0", "type", "dummy")
	// Another program's rules at our prefs must survive us.
	for _, fam := range []string{"-4", "-6"} {
		sh(fam, "rule", "add", "pref", prefBypass, "fwmark", "0x1", "lookup", "main")
		sh(fam, "rule", "add", "pref", prefBypassDeny, "fwmark", "0x2", "unreachable")
	}

	l := linuxOS{}
	if err := l.up("tm0", netip.MustParseAddr("198.18.0.1"), netip.MustParsePrefix("198.18.0.0/15"), 1500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.close("tm0") })
	for _, p := range []string{"192.0.2.0/24", "100.64.0.10/32", "2001:db8:2::/64", "fd7a:115c:a1e0::10/128"} {
		if err := l.addRoute("tm0", netip.MustParsePrefix(p)); err != nil {
			t.Fatal(err)
		}
	}

	// get reports the device a lookup resolves to, or "unreachable".
	get := func(dst, mark string) string {
		args := []string{"route", "get", dst}
		if netip.MustParseAddr(dst).Is6() {
			args = append([]string{"-6"}, args...)
		}
		if mark != "" {
			args = append(args, "mark", mark)
		}
		out, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			return "unreachable"
		}
		f := strings.Fields(string(out))
		for i, w := range f {
			if w == "dev" && i+1 < len(f) {
				return f[i+1]
			}
		}
		return string(out)
	}
	const tsnet = "0x80000" // what tsnet's netns sets on its sockets
	check := func(name, dst, mark, want string) {
		t.Helper()
		if got := get(dst, mark); got != want {
			t.Errorf("%s: %s mark %q went to %s, want %s", name, dst, mark, got, want)
		}
	}
	verify := func(state string) {
		t.Helper()
		check(state+" app to subnet", "192.0.2.5", "", "tm0")
		check(state+" app to peer", "100.64.0.10", "", "tm0")
		// The loop: tsnet's WireGuard to a LAN endpoint the TUN routes.
		check(state+" tsnet to subnet", "192.0.2.5", tsnet, "unreachable")
		check(state+" tsnet to peer", "100.64.0.10", tsnet, "unreachable")
		check(state+" tsnet to internet", "1.1.1.1", tsnet, "wan0")
		check(state+" tsnet to local net", "203.0.113.50", tsnet, "wan0")
		// Other marks (a VPN's own) are none of our business.
		check(state+" other mark to subnet", "192.0.2.5", "0x3", "tm0")
		// The same over IPv6.
		check(state+" app to subnet (IPv6)", "2001:db8:2::5", "", "tm0")
		check(state+" app to peer (IPv6)", "fd7a:115c:a1e0::10", "", "tm0")
		check(state+" tsnet to subnet (IPv6)", "2001:db8:2::5", tsnet, "unreachable")
		check(state+" tsnet to peer (IPv6)", "fd7a:115c:a1e0::10", tsnet, "unreachable")
		check(state+" tsnet to internet (IPv6)", "2606:4700::1111", tsnet, "wan0")
	}
	verify("tun")

	if err := l.setExit("tm0", true); err != nil {
		t.Fatal(err)
	}
	verify("exit")
	check("exit app to internet", "1.1.1.1", "", "tm0")
	if err := l.setExit("tm0", false); err != nil {
		t.Fatal(err)
	}
	verify("exit off")

	// Coming up again (crash, repair) must not stack rules.
	if err := l.up("tm0", netip.MustParseAddr("198.18.0.1"), netip.MustParsePrefix("198.18.0.0/15"), 1500); err != nil {
		t.Fatal(err)
	}
	for _, fam := range []string{"-4", "-6"} {
		for _, pref := range []string{prefBypass, prefBypassDeny} {
			out, _ := exec.Command("ip", fam, "rule", "show", "pref", pref).Output()
			if n := strings.Count(string(out), "\n"); n != 2 { // ours and the other program's
				t.Errorf("ip %s: %d rules at pref %s after a second up, want 2:\n%s", fam, n, pref, out)
			}
		}
	}

	l.close("tm0")
	for _, fam := range []string{"-4", "-6"} {
		out, _ := exec.Command("ip", fam, "rule", "show").Output()
		if strings.Contains(string(out), "suppress_ifgroup") || strings.Contains(string(out), "0x80000/0xff0000") {
			t.Errorf("ip %s: rules left after close:\n%s", fam, out)
		}
		for _, other := range []string{"fwmark 0x1 lookup main", "fwmark 0x2 unreachable"} {
			if !strings.Contains(string(out), other) {
				t.Errorf("ip %s: another program's rule %q removed:\n%s", fam, other, out)
			}
		}
	}
}
