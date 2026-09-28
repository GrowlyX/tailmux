// Package lab runs complete tailnets in-process for tests: each has
// Tailscale's own test control server, a DERP relay and a few nodes.
package lab

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/logger"
)

// Tailnet is a complete tailnet in-process: its own control server,
// DERP relay and a few nodes.
type Tailnet struct {
	Name    string
	Domain  string
	Control *testcontrol.Server
	URL     string
}

func NewTailnet(t *testing.T, name string) *Tailnet {
	derpMap := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")
	f := &Tailnet{Name: name, Domain: name + ".example.ts.net"}
	f.Control = &testcontrol.Server{
		DERPMap:        derpMap,
		DNSConfig:      &tailcfg.DNSConfig{Proxied: true},
		MagicDNSDomain: f.Domain,
		Logf:           logger.Discard,
	}
	f.Control.HTTPTestServer = httptest.NewUnstartedServer(f.Control)
	f.Control.HTTPTestServer.Start()
	t.Cleanup(f.Control.HTTPTestServer.Close)
	f.URL = f.Control.HTTPTestServer.URL
	return f
}

func (f *Tailnet) Node(t *testing.T, ctx context.Context, host string) (*tsnet.Server, netip.Addr) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), f.Name, host)
	os.MkdirAll(dir, 0o755)
	s := &tsnet.Server{Dir: dir, ControlURL: f.URL, Hostname: host, Store: new(mem.Store), Ephemeral: true, Logf: logger.Discard}
	t.Cleanup(func() { s.Close() })
	st, err := s.Up(ctx)
	if err != nil {
		t.Fatalf("%s/%s up: %v", f.Name, host, err)
	}
	return s, st.TailscaleIPs[0]
}

// webNode serves "<tailnet> web" over HTTP on :80.
func (f *Tailnet) WebNode(t *testing.T, ctx context.Context) netip.Addr {
	s, ip := f.Node(t, ctx, "web")
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ?bytes=N serves N bytes, for throughput demos.
		if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil && n > 0 {
			w.Write(bytes.Repeat([]byte{'x'}, min(n, 64<<20)))
			return
		}
		fmt.Fprintf(w, "%s web", f.Name)
	}))
	return ip
}

// gateway advertises subnet routes and answers any TCP connection into
// them with "<tailnet> gw <dst>".
func (f *Tailnet) Gateway(t *testing.T, ctx context.Context, routes ...string) {
	s, _ := f.Node(t, ctx, "gw")
	var pfxs []netip.Prefix
	for _, r := range routes {
		pfxs = append(pfxs, netip.MustParsePrefix(r))
	}
	s.RegisterFallbackTCPHandler(func(src, dst netip.AddrPort) (func(net.Conn), bool) {
		return func(c net.Conn) {
			fmt.Fprintf(c, "%s gw %s\n", f.Name, dst)
			c.Close()
		}, true
	})
	lc, _ := s.LocalClient()
	if _, err := lc.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{AdvertiseRoutes: pfxs}, AdvertiseRoutesSet: true}); err != nil {
		t.Fatal(err)
	}
	st, _ := lc.Status(ctx)
	f.Control.SetSubnetRoutes(st.Self.PublicKey, pfxs)
}

// splitDNS runs a resolver node answering *.<zone> with answer, and
// tells control to route that zone to it.
//
// It must run before any other node joins: testcontrol's DNSConfig has
// no lock, so it's set up front with the address control hands the
// first node, and checked afterwards.
func (f *Tailnet) SplitDNS(t *testing.T, ctx context.Context, zone string, answer netip.Addr) {
	want := netip.MustParseAddr("100.64.0.1")
	f.Control.DNSConfig = &tailcfg.DNSConfig{
		Proxied: true,
		Routes:  map[string][]*dnstype.Resolver{zone: {{Addr: want.String()}}},
	}
	s, ip := f.Node(t, ctx, "resolver")
	if ip != want {
		t.Fatalf("resolver got %v, want %v", ip, want)
	}
	pc, err := s.ListenPacket("udp", netip.AddrPortFrom(ip, 53).String())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			if q.Type == dnsmessage.TypeA && strings.HasSuffix(q.Name.String(), "."+zone+".") {
				b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: answer.As4()})
			}
			out, _ := b.Finish()
			pc.WriteTo(out, addr)
		}
	}()
}

// Start brings up three tailnets that deliberately collide: every
// "web" node gets 100.64.0.1, alpha and bravo both route 192.0.2.0/24,
// and charlie serves corp.internal over split DNS.
func Start(t *testing.T, ctx context.Context) (alpha, bravo, charlie *Tailnet, webIPs map[string]netip.Addr) {
	alpha, bravo, charlie = NewTailnet(t, "alpha"), NewTailnet(t, "bravo"), NewTailnet(t, "charlie")
	charlie.SplitDNS(t, ctx, "corp.internal", netip.MustParseAddr("203.0.113.200"))
	webIPs = map[string]netip.Addr{}
	for _, f := range []*Tailnet{alpha, bravo, charlie} {
		webIPs[f.Name] = f.WebNode(t, ctx)
	}
	// alpha and bravo both claim 192.0.2.0/24, like two homelabs on the
	// same default router subnet. Everything uses documentation ranges
	// (RFC 5737) so it never overlaps a real local network.
	alpha.Gateway(t, ctx, "192.0.2.0/24", "198.51.100.0/25")
	bravo.Gateway(t, ctx, "192.0.2.0/24", "198.51.100.128/25")
	charlie.Gateway(t, ctx, "203.0.113.0/25", "203.0.113.128/25")
	t.Logf("web IPs: %v (same-IP collisions across tailnets are expected)", webIPs)

	return
}

func Eventually(t *testing.T, what string, timeout time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = f(); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: %v", what, err)
}

// WarmUp waits until every lab tailnet is reachable end to end through
// dial: the first WireGuard handshakes can take a while on a loaded CI
// machine, and tests shouldn't spend their own timeouts on that.
func WarmUp(t *testing.T, dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	t.Helper()
	for _, addr := range []string{"web.alpha:80", "web.bravo:80", "web.charlie:80", "198.51.100.3:22", "198.51.100.137:22", "203.0.113.9:22"} {
		Eventually(t, "warm up "+addr, 3*time.Minute, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			c, err := dial(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return c.Close()
		})
	}
}
