package mux

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GrowlyX/tailmux/internal/lab"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
	"tailscale.com/net/netns"
)

func TestEndToEnd(t *testing.T) {
	log.SetOutput(io.MultiWriter(os.Stderr, Logs))
	defer log.SetOutput(os.Stderr)
	if testing.Short() {
		t.Skip("spins up three tailnets")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	alpha, bravo, charlie, webIPs := lab.Start(t, ctx)

	cfg := &Config{
		StateDir: t.TempDir(),
		Hostname: "tailmux",
		SOCKS5:   "127.0.0.1:0",
		HTTP:     "127.0.0.1:0",
		Tailnets: []TailnetConfig{
			{Name: "alpha", ControlURL: alpha.URL, Ephemeral: true},
			{Name: "bravo", ControlURL: bravo.URL, Ephemeral: true},
			{Name: "charlie", ControlURL: charlie.URL, Ephemeral: true},
		},
		Pins: map[string]string{"192.0.2.128/25": "bravo"},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	// The config as a user would have written it, for the management API.
	m.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(m.ConfigPath, []byte(fmt.Sprintf(`{"tailnets":[{"name":"alpha","control_url":%q},{"name":"bravo","control_url":%q},{"name":"charlie","control_url":%q}]}`, alpha.URL, bravo.URL, charlie.URL)), 0o600)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}

	lab.Eventually(t, "all tailnets running with routes and split DNS", 90*time.Second, func() error {
		for _, s := range m.Router().Snapshots() {
			routes, peers := 0, 0
			for _, p := range s.Peers {
				routes += len(p.Routes)
				peers++
			}
			if !s.Running || routes == 0 || peers < 2 {
				return fmt.Errorf("%s: running=%v peers=%d routes=%d", s.Name, s.Running, peers, routes)
			}
			if s.Name == "charlie" && len(s.SplitDNS) == 0 {
				return fmt.Errorf("charlie: no split DNS yet")
			}
		}
		return nil
	})

	lab.WarmUp(t, func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _, err := m.DialTailnet(ctx, network, addr)
		return c, err
	})
	socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	dnsPC, _ := net.ListenPacket("udp", "127.0.0.1:0")
	go m.ServeSOCKS5(socksLn)
	go http.Serve(httpLn, m.HTTPHandler(socksLn.Addr().String()))
	go m.ServeDNS(dnsPC)
	t.Cleanup(func() { socksLn.Close(); httpLn.Close(); dnsPC.Close() })

	socks, err := proxy.SOCKS5("tcp", socksLn.Addr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	socksHTTP := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		DialContext: socks.(proxy.ContextDialer).DialContext, DisableKeepAlives: true,
	}}
	getVia := func(c *http.Client, u string) (string, error) {
		resp, err := c.Get(u)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}

	t.Run("web by name, all tailnets at once", func(t *testing.T) {
		cases := map[string]string{
			"http://web.alpha/":                   "alpha web", // host.<tailnet> alias
			"http://web.bravo/":                   "bravo web",
			"http://web.charlie/":                 "charlie web",
			"http://web.bravo.example.ts.net/":    "bravo web", // full MagicDNS name
			"http://web.charlie.example.ts.net./": "charlie web",
			"http://web/":                         "alpha web", // bare name: priority
		}
		for u, want := range cases {
			lab.Eventually(t, u, 30*time.Second, func() error {
				got, err := getVia(socksHTTP, u)
				if err != nil {
					return err
				}
				if got != want {
					return fmt.Errorf("got %q, want %q", got, want)
				}
				return nil
			})
		}
	})

	gw := func(addr string) (string, error) {
		c, err := socks.Dial("tcp", addr)
		if err != nil {
			return "", err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(line), err
	}
	t.Run("subnets across tailnets", func(t *testing.T) {
		cases := map[string]string{
			"198.51.100.3:22":     "alpha gw 198.51.100.3:22",
			"198.51.100.137:5432": "bravo gw 198.51.100.137:5432",
			"203.0.113.10:443":    "charlie gw 203.0.113.10:443",
			"192.0.2.5:80":        "alpha gw 192.0.2.5:80",   // contested /24: priority
			"192.0.2.200:80":      "bravo gw 192.0.2.200:80", // pinned /25
		}
		for addr, want := range cases {
			lab.Eventually(t, addr, 30*time.Second, func() error {
				got, err := gw(addr)
				if err != nil {
					return err
				}
				if got != want {
					return fmt.Errorf("got %q, want %q", got, want)
				}
				return nil
			})
		}
	})

	t.Run("split DNS", func(t *testing.T) {
		lab.Eventually(t, "db.corp.internal", 30*time.Second, func() error {
			got, err := gw("db.corp.internal:5432")
			if err != nil {
				return err
			}
			if want := "charlie gw 203.0.113.200:5432"; got != want {
				return fmt.Errorf("got %q, want %q", got, want)
			}
			return nil
		})
	})

	t.Run("split DNS pointing outside the tailnet goes direct", func(t *testing.T) {
		for _, snap := range m.Router().Snapshots() {
			if slices.Contains(snap.SplitDNS, "ts.net") {
				t.Errorf("%s claims ts.net", snap.Name)
			}
		}
		tgt, err := m.Resolve(context.Background(), "public.corp.internal")
		if err != nil || tgt.Tailnet != "" || len(tgt.IPs) == 0 || tgt.IPs[0] != netip.MustParseAddr("127.0.0.1") {
			t.Fatalf("got %+v %v, want direct to 127.0.0.1", tgt, err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "public") }))
		defer srv.Close()
		_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		if got, err := getVia(socksHTTP, "http://public.corp.internal:"+port+"/"); err != nil || got != "public" {
			t.Fatalf("via proxy: %q %v", got, err)
		}
	})

	t.Run("http proxy", func(t *testing.T) {
		pu, _ := url.Parse("http://" + httpLn.Addr().String())
		c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
		got, err := getVia(c, "http://web.charlie/")
		if err != nil || got != "charlie web" {
			t.Fatalf("forward proxy: %q, %v", got, err)
		}
		// CONNECT tunnel, raw.
		conn, err := net.Dial("tcp", httpLn.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "CONNECT 198.51.100.201:9000 HTTP/1.1\r\nHost: 198.51.100.201:9000\r\n\r\n")
		br := bufio.NewReader(conn)
		// After a CONNECT the "body" is the tunnel itself, read through br
		// below; closing it would consume the stream.
		resp, err := http.ReadResponse(br, nil) //nolint:bodyclose
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("CONNECT: %v %v", resp, err)
		}
		line, _ := br.ReadString('\n')
		if strings.TrimSpace(line) != "bravo gw 198.51.100.201:9000" {
			t.Fatalf("CONNECT payload %q", line)
		}
	})

	t.Run("direct fallback", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local") }))
		defer srv.Close()
		got, err := getVia(socksHTTP, srv.URL)
		if err != nil || got != "local" {
			t.Fatalf("direct: %q %v", got, err)
		}
	})

	t.Run("dns server", func(t *testing.T) {
		for tn, want := range webIPs {
			got := queryA(t, dnsPC.LocalAddr().String(), "web."+tn+".")
			if got != want {
				t.Errorf("web.%s A = %v, want %v", tn, got, want)
			}
		}
		if got := queryA(t, dnsPC.LocalAddr().String(), "example.com."); got.IsValid() {
			t.Errorf("example.com should be NXDOMAIN, got %v", got)
		}
	})

	t.Run("disable, enable and stats", func(t *testing.T) {
		base := "http://" + httpLn.Addr().String()
		if code := toggle(t, base, "bravo", "disable", false); code != http.StatusForbidden {
			t.Fatalf("toggle without X-Tailmux header: %d, want 403", code)
		}
		if code := toggle(t, base, "bravo", "disable", true); code != 200 {
			t.Fatalf("disable: %d", code)
		}
		if d := m.Router().RouteIP(netip.MustParseAddr("198.51.100.137")); d.OK() {
			t.Errorf("bravo's subnet still routed while disabled: %+v", d)
		}
		if _, _, err := m.DialTailnet(context.Background(), "tcp", "web.bravo:80"); err == nil {
			t.Error("web.bravo reachable while bravo is disabled")
		}
		if d := m.Router().RouteIP(netip.MustParseAddr("192.0.2.200")); d.Tailnet != "alpha" {
			t.Errorf("pin to disabled bravo should fall back to alpha, got %+v", d)
		}
		if got, err := getVia(socksHTTP, "http://web/"); err != nil || got != "alpha web" {
			t.Errorf("web: %q %v", got, err)
		}
		if code := toggle(t, base, "bravo", "enable", true); code != 200 {
			t.Fatalf("enable: %d", code)
		}
		lab.Eventually(t, "bravo back", 30*time.Second, func() error {
			got, err := gw("198.51.100.137:22")
			if err != nil {
				return err
			}
			if got != "bravo gw 198.51.100.137:22" {
				return fmt.Errorf("got %q", got)
			}
			return nil
		})
		if code := toggle(t, base, "nope", "disable", true); code != 404 {
			t.Errorf("unknown tailnet: %d", code)
		}
		lab.Eventually(t, "stats", 5*time.Second, func() error {
			for _, ts := range m.Stats().Tailnets {
				if ts.Name == "bravo" && (ts.RxTotal == 0 || ts.TxTotal == 0 || len(ts.Rx) == 0) {
					return fmt.Errorf("bravo stats: %+v", ts)
				}
			}
			return nil
		})
	})

	t.Run("manage tailnets and settings over the API", func(t *testing.T) {
		base := "http://" + httpLn.Addr().String()
		call := func(method, path, body string, header bool) (int, string) {
			req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
			if header {
				req.Header.Set("X-Tailmux", "1")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		names := func() []string {
			c, _ := ReadConfig(m.ConfigPath)
			var n []string
			for _, tn := range c.Tailnets {
				n = append(n, tn.Name)
			}
			return n
		}

		// A fourth tailnet, joined while running.
		// Its own budget: under -race the shared test context is mostly
		// spent by the time this runs.
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer dcancel()
		delta := lab.NewTailnet(t, "delta")
		delta.WebNode(t, dctx)
		body := fmt.Sprintf(`{"name":"delta","control_url":%q,"ephemeral":true}`, delta.URL)
		if code, _ := call("POST", "/tailnets", body, false); code != 403 {
			t.Fatalf("add without header: %d", code)
		}
		if code, out := call("POST", "/tailnets", body, true); code != 201 {
			t.Fatalf("add: %d %s", code, out)
		}
		if code, _ := call("POST", "/tailnets", body, true); code != 400 {
			t.Errorf("duplicate add: %d", code)
		}
		if code, _ := call("POST", "/tailnets", `{"name":"no dots.here"}`, true); code != 400 {
			t.Errorf("bad name: %d", code)
		}
		if got := names(); !slices.Equal(got, []string{"alpha", "bravo", "charlie", "delta"}) {
			t.Fatalf("config after add: %v", got)
		}
		lab.Eventually(t, "web.delta reachable", 60*time.Second, func() error {
			got, err := getVia(socksHTTP, "http://web.delta/")
			if err != nil {
				return err
			}
			if got != "delta web" {
				return fmt.Errorf("got %q", got)
			}
			return nil
		})

		if code, _ := call("DELETE", "/tailnets/delta", "", true); code != 204 {
			t.Fatalf("remove: %d", code)
		}
		if code, _ := call("DELETE", "/tailnets/delta", "", true); code != 404 {
			t.Errorf("remove twice: %d", code)
		}
		if got := names(); !slices.Equal(got, []string{"alpha", "bravo", "charlie"}) {
			t.Fatalf("config after remove: %v", got)
		}
		if d := m.Router().RouteName("web.delta"); d.OK() {
			t.Errorf("removed tailnet still routes: %+v", d)
		}

		if code, out := call("PUT", "/settings", `{"tun":true,"auto_update":false,"hostname":"laptop"}`, true); code != 200 || !strings.Contains(out, "restart_required") {
			t.Fatalf("settings: %d %s", code, out)
		}
		c, _ := ReadConfig(m.ConfigPath)
		if !c.TUN.Enabled || c.Updates.AutoEnabled() || c.Hostname != "laptop" {
			t.Errorf("settings not saved: %+v", c)
		}
		if code, out := call("GET", "/config", "", false); code != 200 || !strings.Contains(out, `"laptop"`) {
			t.Errorf("config view: %d %s", code, out)
		}
		log.Printf("hello from the log ring")
		if code, out := call("GET", "/logs?n=50", "", false); code != 200 || !strings.Contains(out, "hello from the log ring") {
			t.Errorf("logs: %d %.200s", code, out)
		}
	})

	t.Run("status, conflicts and PAC", func(t *testing.T) {
		st := m.Status()
		for _, tn := range st.Tailnets {
			t.Logf("%s: %s suffix=%s self=%v peers=%d routes=%v dns=%v", tn.Name, tn.State, tn.Suffix, tn.SelfIPs, tn.Peers, tn.Routes, tn.SplitDNS)
		}
		found := false
		for _, c := range st.Conflicts {
			t.Logf("conflict %s: %v -> %s", c.What, c.Tailnets, c.Winner)
			if c.What == "192.0.2.0/24" && c.Winner == "alpha" {
				found = true
			}
		}
		if !found {
			t.Error("expected 192.0.2.0/24 conflict won by alpha")
		}
		pac := m.PAC("127.0.0.1:1056", "127.0.0.1:1055")
		for _, want := range []string{`"alpha.example.ts.net"`, `"corp.internal"`, `isInNet(host, "198.51.100.128", "255.255.255.128")`} {
			if !strings.Contains(pac, want) {
				t.Errorf("PAC missing %s:\n%s", want, pac)
			}
		}
	})
}

func toggle(t *testing.T, base, name, action string, header bool) int {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/tailnets/"+name+"/"+action, nil)
	if header {
		req.Header.Set("X-Tailmux", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func queryA(t *testing.T, server, name string) netip.Addr {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 42, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	q, _ := b.Finish()
	c, err := net.Dial("udp", server)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	c.Write(q)
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	ips, _ := parseDNSAddrs(buf[:n])
	if len(ips) == 0 {
		return netip.Addr{}
	}
	return ips[0]
}
