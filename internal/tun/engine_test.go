package tun

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/wireguard-go/tun/tuntest"
	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"tailscale.com/net/netns"

	"github.com/GrowlyX/tailmux/internal/lab"
	"github.com/GrowlyX/tailmux/internal/mux"
)

// startMux joins the three lab tailnets and waits until routes and split
// DNS are visible.
func startMux(t *testing.T, ctx context.Context) (*mux.Mux, *mux.Config, map[string]netip.Addr) {
	t.Helper()
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	alpha, bravo, charlie, webIPs := lab.Start(t, ctx)
	cfg := &mux.Config{
		StateDir: t.TempDir(),
		Hostname: "tailmux",
		Tailnets: []mux.TailnetConfig{
			{Name: "alpha", ControlURL: alpha.URL, Ephemeral: true},
			{Name: "bravo", ControlURL: bravo.URL, Ephemeral: true},
			{Name: "charlie", ControlURL: charlie.URL, Ephemeral: true},
		},
		Pins: map[string]string{"192.0.2.128/25": "bravo"},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := mux.New(cfg, mux.Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	lab.Eventually(t, "tailnets ready", 90*time.Second, func() error {
		for _, s := range m.Router().Snapshots() {
			routes := 0
			for _, p := range s.Peers {
				routes += len(p.Routes)
			}
			if !s.Running || routes == 0 || (s.Name == "charlie" && len(s.SplitDNS) == 0) {
				return fmt.Errorf("%s not ready", s.Name)
			}
		}
		return nil
	})
	lab.WarmUp(t, func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _, err := m.DialTailnet(ctx, network, addr)
		return c, err
	})
	return m, cfg, webIPs
}

// osStack is a second userspace stack standing in for the operating
// system: it sends into the TUN exactly what the kernel would.
func osStack(t *testing.T, ctx context.Context, ct *tuntest.ChannelTUN) *stack.Stack {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(256, 1500, "")
	s.CreateNIC(1, ep)
	s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4([4]byte{10, 200, 0, 2}).WithPrefix(),
	}, stack.AddressProperties{})
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	go func() { // OS -> TUN
		for {
			pkt := ep.ReadContext(ctx)
			if pkt == nil {
				return
			}
			v := pkt.ToView()
			b := append([]byte(nil), v.AsSlice()...)
			v.Release()
			pkt.DecRef()
			select {
			case ct.Outbound <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { // TUN -> OS
		for {
			select {
			case b, ok := <-ct.Inbound:
				if !ok {
					return
				}
				pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(b)})
				ep.InjectInbound(ipv4.ProtocolNumber, pb)
				pb.DecRef()
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { ep.Close(); s.Close() })
	return s
}

func full(ip netip.Addr, port uint16) tcpip.FullAddress {
	return tcpip.FullAddress{Addr: tcpip.AddrFrom4(ip.As4()), Port: port}
}

func dnsQuery(t *testing.T, c net.Conn, name string) (netip.Addr, dnsmessage.RCode) {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	q, _ := b.Finish()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("dns %s: %v", name, err)
	}
	var p dnsmessage.Parser
	h, err := p.Start(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	p.SkipAllQuestions()
	for {
		ah, err := p.AnswerHeader()
		if err != nil {
			return netip.Addr{}, h.RCode
		}
		if ah.Type == dnsmessage.TypeA {
			r, _ := p.AResource()
			return netip.AddrFrom4(r.A), h.RCode
		}
		p.SkipAnswer()
	}
}

func TestEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up three tailnets")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	m, cfg, _ := startMux(t, ctx)

	ct := tuntest.NewChannelTUN()
	fake := NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), "")
	e, err := NewEngine(m, ct.TUN(), fake, 1500)
	if err != nil {
		t.Fatal(err)
	}
	go e.Run(ctx)
	os := osStack(t, ctx, ct)

	lookup := func(name string) (netip.Addr, dnsmessage.RCode) {
		c, err := gonet.DialUDP(os, nil, &tcpip.FullAddress{Addr: tcpip.AddrFrom4(fake.DNS().As4()), Port: 53}, ipv4.ProtocolNumber)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return dnsQuery(t, c, name)
	}
	dialTCP := func(ip netip.Addr, port uint16) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return gonet.DialContextTCP(ctx, os, full(ip, port), ipv4.ProtocolNumber)
	}
	line := func(ip netip.Addr, port uint16) string {
		t.Helper()
		c, err := dialTCP(ip, port)
		if err != nil {
			t.Fatalf("dial %v:%d: %v", ip, port, err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		s, _ := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(s)
	}

	t.Run("fake IPs keep colliding tailnets apart", func(t *testing.T) {
		seen := map[netip.Addr]string{}
		for _, tn := range []string{"alpha", "bravo", "charlie"} {
			ip, rc := lookup("web." + tn + ".")
			if rc != dnsmessage.RCodeSuccess || !fake.Prefix().Contains(ip) {
				t.Fatalf("web.%s: %v %v", tn, ip, rc)
			}
			if other, dup := seen[ip]; dup {
				t.Fatalf("web.%s and web.%s share %v", tn, other, ip)
			}
			seen[ip] = tn
			hc := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
				DialContext: func(context.Context, string, string) (net.Conn, error) { return dialTCP(ip, 80) },
			}}
			resp, err := hc.Get("http://web." + tn + "/")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if want := tn + " web"; string(body) != want {
				t.Errorf("web.%s: got %q, want %q", tn, body, want)
			}
		}
	})

	t.Run("NXDOMAIN for names nobody has", func(t *testing.T) {
		for _, n := range []string{"nosuchhost.bravo.", "example.com.", "nope.bravo.example.ts.net."} {
			if ip, rc := lookup(n); rc != dnsmessage.RCodeNameError {
				t.Errorf("%s: %v %v, want NXDOMAIN", n, ip, rc)
			}
		}
	})

	t.Run("real subnet IPs", func(t *testing.T) {
		cases := map[string]string{
			"198.51.100.3":   "alpha gw 198.51.100.3:22",
			"198.51.100.137": "bravo gw 198.51.100.137:22",
			"203.0.113.9":    "charlie gw 203.0.113.9:22",
			"192.0.2.200":    "bravo gw 192.0.2.200:22", // pinned
		}
		for ip, want := range cases {
			if got := line(netip.MustParseAddr(ip), 22); got != want {
				t.Errorf("%s: got %q, want %q", ip, got, want)
			}
		}
	})

	t.Run("split DNS name", func(t *testing.T) {
		ip, rc := lookup("db.corp.internal.")
		if rc != dnsmessage.RCodeSuccess {
			t.Fatal(rc)
		}
		if got, want := line(ip, 5432), "charlie gw 203.0.113.200:5432"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("off-net split DNS gets real IPs", func(t *testing.T) {
		ip, rc := lookup("public.corp.internal.")
		if rc != dnsmessage.RCodeSuccess || ip != netip.MustParseAddr("127.0.0.1") {
			t.Fatalf("got %v %v, want the real 127.0.0.1 (not a fake IP)", ip, rc)
		}
	})

	t.Run("UDP through the tunnel", func(t *testing.T) {
		// Ask charlie's in-tailnet resolver directly, by name, over UDP.
		rip, rc := lookup("resolver.charlie.")
		if rc != dnsmessage.RCodeSuccess {
			t.Fatal(rc)
		}
		c, err := gonet.DialUDP(os, nil, &tcpip.FullAddress{Addr: tcpip.AddrFrom4(rip.As4()), Port: 53}, ipv4.ProtocolNumber)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if got, _ := dnsQuery(t, c, "db.corp.internal."); got != netip.MustParseAddr("203.0.113.200") {
			t.Errorf("got %v", got)
		}
	})

	t.Run("unclaimed destinations are refused, not dialed directly", func(t *testing.T) {
		c, err := dialTCP(netip.MustParseAddr("8.8.8.8"), 80)
		if err == nil {
			c.Close()
			t.Fatal("expected refusal")
		}
		unknownFake := netip.MustParseAddr("198.19.255.200")
		if c, err := dialTCP(unknownFake, 80); err == nil {
			c.Close()
			t.Fatal("expected refusal for unallocated fake IP")
		}
	})

	t.Run("real IPs when no other tailnet has them", func(t *testing.T) {
		e.routed = func(netip.Addr) bool { return true }
		defer func() { e.routed = nil }()
		// Every lab tailnet numbers its web node 100.64.0.1: still fake.
		if ip, rc := lookup("web.bravo."); rc != dnsmessage.RCodeSuccess || !fake.Prefix().Contains(ip) {
			t.Errorf("web.bravo: %v %v, want a fake address", ip, rc)
		}
		// charlie's gateway has an address no other tailnet uses.
		want := m.Router().RouteName("gw.charlie").IPs[0]
		if ip, rc := lookup("gw.charlie."); rc != dnsmessage.RCodeSuccess || ip.String() != want {
			t.Errorf("gw.charlie: %v %v, want its real address %s", ip, rc, want)
		}
		// Not routed into the TUN (say it overlaps the LAN): fake again.
		e.routed = func(netip.Addr) bool { return false }
		if ip, _ := lookup("gw.charlie."); !fake.Prefix().Contains(ip) {
			t.Errorf("gw.charlie unrouted: %v, want a fake address", ip)
		}
	})

	t.Run("exit node carries everything else", func(t *testing.T) {
		delta := lab.NewTailnet(t, "delta")
		delta.ExitNode(t, ctx, "exit")
		if _, err := m.AddTailnet(mux.TailnetConfig{Name: "delta", ControlURL: delta.URL, Ephemeral: true}); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveTailnet("delta")
		lab.Eventually(t, "delta's exit node", 90*time.Second, func() error {
			_, err := m.SetExitNode(ctx, "delta", "exit")
			return err
		})
		defer m.SetExitNode(ctx, "", "")
		lab.Eventually(t, "8.8.8.8 through the exit node", 60*time.Second, func() error {
			c, err := dialTCP(netip.MustParseAddr("8.8.8.8"), 80)
			if err != nil {
				return err
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			got, _ := bufio.NewReader(c).ReadString('\n')
			if want := "delta exit 8.8.8.8:80"; strings.TrimSpace(got) != want {
				return fmt.Errorf("got %q, want %q", got, want)
			}
			return nil
		})
		if got := line(netip.MustParseAddr("198.51.100.3"), 22); got != "alpha gw 198.51.100.3:22" {
			t.Errorf("subnet route: got %q", got)
		}
		// Public names resolve through the exit node (which here uses this
		// host's resolver, so it needs the internet).
		if _, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", "example.com"); err == nil {
			if ip, rc := lookup("example.com."); rc != dnsmessage.RCodeSuccess || !ip.IsValid() || fake.Prefix().Contains(ip) {
				t.Errorf("example.com: %v %v, want a real address", ip, rc)
			}
		}
		if ip, rc := lookup("web.bravo."); rc != dnsmessage.RCodeSuccess || !fake.Prefix().Contains(ip) {
			t.Errorf("web.bravo: %v %v, want a fake address", ip, rc)
		}
	})
}

func TestFakeIPs(t *testing.T) {
	path := t.TempDir() + "/fake.json"
	f := NewFakeIPs(netip.MustParsePrefix("198.18.0.0/15"), path)
	a, b := f.For("web.alpha"), f.For("WEB.bravo.")
	if a == b || f.For("web.alpha.") != a {
		t.Fatalf("allocation: %v %v", a, b)
	}
	if a != netip.MustParseAddr("198.18.1.0") || f.DNS() != netip.MustParseAddr("198.18.0.53") || f.Gateway() != netip.MustParseAddr("198.18.0.1") {
		t.Fatalf("layout: %v %v %v", a, f.DNS(), f.Gateway())
	}
	if n, ok := f.Name(b); !ok || n != "web.bravo" {
		t.Fatalf("reverse: %q %v", n, ok)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	g := NewFakeIPs(netip.MustParsePrefix("198.18.0.0/15"), path)
	if g.For("web.bravo") != b || g.For("new.name") == a {
		t.Fatal("state not restored")
	}
	// Wraparound reuses addresses and forgets the old owner.
	small := NewFakeIPs(netip.MustParsePrefix("10.0.0.0/23"), "")
	first := small.For("n0")
	for i := 1; i < 256; i++ {
		small.For(fmt.Sprint("n", i))
	}
	if small.For("again") != first {
		t.Fatal("expected wraparound")
	}
	if _, ok := small.Name(first); !ok {
		t.Fatal("reused address should map to the new name")
	}
	if n, _ := small.Name(first); n != "again" {
		t.Fatalf("got %q", n)
	}
}
