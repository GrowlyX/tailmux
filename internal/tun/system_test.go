package tun

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/GrowlyX/tailmux/internal/lab"
)

// TestSystem drives a real TUN device through the OS network stack: no
// proxies, plain net.Dial and http.Get. It changes routes and DNS, so it
// only runs as root with TAILMUX_TUN_E2E=1 (CI does this on Linux and
// macOS runners).
func TestSystem(t *testing.T) {
	if os.Getenv("TAILMUX_TUN_E2E") == "" {
		t.Skip("set TAILMUX_TUN_E2E=1 and run as root")
	}
	if !isAdmin() {
		t.Fatal("TestSystem needs root (Administrator on Windows)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	m, cfg, _ := startMux(t, ctx)

	sys, err := Start(ctx, m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			sys.Close()
		}
	})
	t.Logf("tun %s, OS DNS integration: %v", sys.Interface(), sys.DNSActive())

	// Query tailmux's DNS over the kernel, through the TUN.
	dnsAddr := net.JoinHostPort(sys.FakeIPs().DNS().String(), "53")
	fakeFor := func(name string) netip.Addr {
		t.Helper()
		var ip netip.Addr
		lab.Eventually(t, "dns "+name, 20*time.Second, func() error {
			c, err := net.Dial("udp", dnsAddr)
			if err != nil {
				return err
			}
			defer c.Close()
			var rc dnsmessage.RCode
			ip, rc, err = query(c, name)
			if err != nil {
				return err
			}
			if rc != dnsmessage.RCodeSuccess || !ip.IsValid() {
				return fmt.Errorf("%v %v", ip, rc)
			}
			return nil
		})
		return ip
	}
	hc := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	get := func(url string) (string, error) {
		resp, err := hc.Get(url)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}
	readLine := func(addr string) (string, error) {
		c, err := net.DialTimeout("tcp", addr, 20*time.Second)
		if err != nil {
			return "", err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		s, err := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(s), err
	}
	expect := func(t *testing.T, what, want string, f func() (string, error)) {
		t.Helper()
		lab.Eventually(t, what, 30*time.Second, func() error {
			got, err := f()
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("got %q, want %q", got, want)
			}
			return nil
		})
	}

	t.Run("fake IP over the kernel", func(t *testing.T) {
		for _, tn := range []string{"alpha", "bravo", "charlie"} {
			ip := fakeFor("web." + tn + ".")
			expect(t, "web."+tn, tn+" web", func() (string, error) {
				return get("http://" + ip.String() + "/")
			})
		}
	})

	t.Run("subnet routes over the kernel", func(t *testing.T) {
		for addr, want := range map[string]string{
			"198.51.100.3:22":   "alpha gw 198.51.100.3:22",
			"198.51.100.137:22": "bravo gw 198.51.100.137:22",
			"203.0.113.9:22":    "charlie gw 203.0.113.9:22",
			"192.0.2.200:22":    "bravo gw 192.0.2.200:22",
		} {
			expect(t, addr, want, func() (string, error) { return readLine(addr) })
		}
	})

	t.Run("device IPs over the kernel", func(t *testing.T) {
		// What public DNS pointing at a tailnet device looks like: the
		// app connects to the real 100.x address. 100.64.0.1 is web in
		// alpha and bravo; alpha wins on priority.
		expect(t, "http://100.64.0.1/", "alpha web", func() (string, error) { return get("http://100.64.0.1/") })
	})

	t.Run("UDP over the kernel", func(t *testing.T) {
		rip := fakeFor("resolver.charlie.")
		lab.Eventually(t, "udp to in-tailnet resolver", 20*time.Second, func() error {
			c, err := net.Dial("udp", net.JoinHostPort(rip.String(), "53"))
			if err != nil {
				return err
			}
			defer c.Close()
			ip, _, err := query(c, "db.corp.internal.")
			if err != nil {
				return err
			}
			if ip != netip.MustParseAddr("203.0.113.200") {
				return fmt.Errorf("got %v", ip)
			}
			return nil
		})
	})

	t.Run("ping over the kernel", func(t *testing.T) {
		ip := fakeFor("web.charlie.")
		lab.Eventually(t, "ping web.charlie", 60*time.Second, func() error {
			var args []string
			switch runtime.GOOS {
			case "darwin":
				args = []string{"-c", "1", "-t", "5"}
			case "windows":
				args = []string{"-n", "1", "-w", "5000"}
			default:
				args = []string{"-c", "1", "-W", "5"}
			}
			out, err := exec.Command("ping", append(args, ip.String())...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%v: %s", err, out)
			}
			return nil
		})
	})

	t.Run("repairs what the OS dropped", func(t *testing.T) {
		// What a sleep/wake or network change can do: routes and the
		// per-domain DNS config vanish behind tailmux's back.
		gone := netip.MustParsePrefix("198.51.100.128/25")
		if err := sys.os.delRoute(sys.Interface(), gone); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "darwin" {
			os.Remove("/etc/resolver/bravo")
		}
		if c, err := net.DialTimeout("tcp", "198.51.100.137:22", 2*time.Second); err == nil {
			c.Close()
			t.Fatal("route removal had no effect; test is meaningless")
		}
		sys.Repair("test")
		expect(t, "after repair", "bravo gw 198.51.100.137:22", func() (string, error) { return readLine("198.51.100.137:22") })
		if runtime.GOOS == "darwin" {
			if _, err := os.Stat("/etc/resolver/bravo"); err != nil {
				t.Errorf("resolver file not restored: %v", err)
			}
		}
	})

	t.Run("system resolver", func(t *testing.T) {
		if !sys.DNSActive() {
			if os.Getenv("TAILMUX_TUN_E2E_REQUIRE_DNS") != "" {
				t.Fatal("OS DNS integration unavailable but TAILMUX_TUN_E2E_REQUIRE_DNS is set")
			}
			t.Skipf("no per-domain DNS on this host (%s without systemd-resolved)", runtime.GOOS)
		}
		// Plain hostnames, resolved by the OS: this is the whole point.
		expect(t, "http://web.bravo/", "bravo web", func() (string, error) { return get("http://web.bravo/") })
		expect(t, "http://web.charlie.example.ts.net/", "charlie web", func() (string, error) { return get("http://web.charlie.example.ts.net/") })
		expect(t, "db.corp.internal:5432", "charlie gw 203.0.113.200:5432", func() (string, error) { return readLine("db.corp.internal:5432") })
	})

	t.Run("cleanup", func(t *testing.T) {
		closed = true
		if err := sys.Close(); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "darwin" {
			if _, err := os.Stat("/etc/resolver/bravo"); !os.IsNotExist(err) {
				t.Errorf("resolver file left behind: %v", err)
			}
		}
		if _, err := net.InterfaceByName(sys.Interface()); err == nil {
			t.Errorf("interface %s still exists", sys.Interface())
		}
	})
}

func query(c net.Conn, name string) (netip.Addr, dnsmessage.RCode, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 9, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	q, _ := b.Finish()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(q); err != nil {
		return netip.Addr{}, 0, err
	}
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	var p dnsmessage.Parser
	h, err := p.Start(buf[:n])
	if err != nil {
		return netip.Addr{}, 0, err
	}
	p.SkipAllQuestions()
	for {
		ah, err := p.AnswerHeader()
		if err != nil {
			return netip.Addr{}, h.RCode, nil
		}
		if ah.Type == dnsmessage.TypeA {
			r, _ := p.AResource()
			return netip.AddrFrom4(r.A), h.RCode, nil
		}
		p.SkipAnswer()
	}
}
