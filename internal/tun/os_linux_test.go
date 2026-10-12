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
	sh("link", "add", "tm0", "type", "dummy")

	l := linuxOS{}
	if err := l.up("tm0", netip.MustParseAddr("198.18.0.1"), netip.MustParsePrefix("198.18.0.0/15"), 1500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.close("tm0") })
	for _, p := range []string{"192.0.2.0/24", "100.64.0.10/32"} {
		if err := l.addRoute("tm0", netip.MustParsePrefix(p)); err != nil {
			t.Fatal(err)
		}
	}

	// get reports the device a lookup resolves to, or "unreachable".
	get := func(dst, mark string) string {
		args := []string{"route", "get", dst}
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
		check(state+" other mark to subnet", "192.0.2.5", "0x1", "tm0")
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
	out, _ := exec.Command("ip", "rule", "show", "pref", prefBypass).Output()
	if n := strings.Count(string(out), "\n"); n != 1 {
		t.Errorf("%d bypass rules after a second up, want 1:\n%s", n, out)
	}

	l.close("tm0")
	out, _ = exec.Command("ip", "rule", "show").Output()
	if strings.Contains(string(out), "suppress_ifgroup") || strings.Contains(string(out), "unreachable") {
		t.Errorf("rules left after close:\n%s", out)
	}
}
