package mux

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GrowlyX/tailmux/internal/lab"
	"tailscale.com/net/netns"
)

func TestExitNode(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up two tailnets")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	alpha, bravo := lab.NewTailnet(t, "alpha"), lab.NewTailnet(t, "bravo")
	alpha.WebNode(t, ctx)
	alpha.ExitNode(t, ctx, "exit-a")
	bravo.ExitNode(t, ctx, "exit-b")
	bravo.Gateway(t, ctx, "192.0.2.0/24")

	cfg := &Config{
		StateDir: t.TempDir(),
		Hostname: "tailmux",
		Tailnets: []TailnetConfig{
			{Name: "alpha", ControlURL: alpha.URL, Ephemeral: true},
			{Name: "bravo", ControlURL: bravo.URL, Ephemeral: true},
		},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	m.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(m.ConfigPath, []byte(fmt.Sprintf(`{"tailnets":[{"name":"alpha","control_url":%q},{"name":"bravo","control_url":%q}]}`, alpha.URL, bravo.URL)), 0o600)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}

	lab.Eventually(t, "both exit nodes listed", 90*time.Second, func() error {
		var names []string
		for _, e := range m.ExitNodes() {
			names = append(names, e.Tailnet+"/"+e.Name)
		}
		if len(names) != 2 {
			return fmt.Errorf("exit nodes: %v", names)
		}
		return nil
	})

	// 1.2.3.4 is in no tailnet, so it's "the internet".
	read := func(addr string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		c, tgt, err := m.DialTailnet(ctx, "tcp", addr)
		if err != nil {
			return "", err
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(tgt.Decision.Peer + " | " + line), err
	}

	if _, err := read("1.2.3.4:80"); err == nil {
		t.Fatal("no exit node: a TUN dial to the internet should fail")
	}

	if _, err := m.SetExitNode(ctx, "alpha", "web"); err == nil {
		t.Fatal("web isn't an exit node; want an error")
	}
	st, err := m.SetExitNode(ctx, "alpha", "exit-a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Node, "exit-a.") {
		t.Fatalf("saved node %q, want the MagicDNS name", st.Node)
	}
	lab.Eventually(t, "internet via alpha's exit node", 60*time.Second, func() error {
		got, err := read("1.2.3.4:80")
		if err != nil {
			return err
		}
		if !strings.HasSuffix(got, "alpha exit-a 1.2.3.4:80") {
			return fmt.Errorf("got %q", got)
		}
		return nil
	})

	t.Run("DNS for other names goes through the exit node", func(t *testing.T) {
		// The exit node answers with its own (here: this host's) resolver,
		// which needs the internet.
		if _, err := net.DefaultResolver.LookupNetIP(ctx, "ip", "example.com"); err != nil {
			t.Skipf("no internet DNS here: %v", err)
		}
		tgt, err := m.Resolve(ctx, "example.com")
		if err != nil || len(tgt.IPs) == 0 || tgt.Tailnet != "" {
			t.Fatalf("got %v, %v", tgt, err)
		}
	})

	t.Run("tailnet destinations still go to their tailnet", func(t *testing.T) {
		got, err := read("192.0.2.7:22")
		if err != nil || !strings.HasSuffix(got, "bravo gw 192.0.2.7:22") {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("loopback stays local", func(t *testing.T) {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		go func() {
			c, err := ln.Accept()
			if err == nil {
				fmt.Fprintln(c, "local")
				c.Close()
			}
		}()
		c, tgt, err := m.Dial(ctx, "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		if tgt.Tailnet != "" {
			t.Fatalf("loopback went via %s", tgt)
		}
	})

	t.Run("ping through the exit node", func(t *testing.T) {
		// The lab's exit node pings with this host's network.
		if c, err := net.DialTimeout("tcp", "8.8.8.8:53", 3*time.Second); err != nil {
			t.Skipf("no internet here: %v", err)
		} else {
			c.Close()
		}
		lab.Eventually(t, "ping 8.8.8.8", 30*time.Second, func() error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return m.Ping(ctx, "8.8.8.8")
		})
	})

	t.Run("switch tailnets", func(t *testing.T) {
		if _, err := m.SetExitNode(ctx, "bravo", "exit-b"); err != nil {
			t.Fatal(err)
		}
		lab.Eventually(t, "internet via bravo's exit node", 60*time.Second, func() error {
			got, err := read("1.2.3.4:443")
			if err != nil {
				return err
			}
			if !strings.HasSuffix(got, "bravo exit-b 1.2.3.4:443") {
				return fmt.Errorf("got %q", got)
			}
			return nil
		})
		if s := m.Status().ExitNode; s == nil || s.Tailnet != "bravo" || !s.Active {
			t.Fatalf("status: %+v", s)
		}
		saved, _ := ReadConfig(m.ConfigPath)
		if saved.ExitNode == nil || saved.ExitNode.Tailnet != "bravo" {
			t.Fatalf("saved: %+v", saved.ExitNode)
		}
	})

	t.Run("fails closed when the exit tailnet is off", func(t *testing.T) {
		if err := m.Tailnet("bravo").setEnabled(ctx, false); err != nil {
			t.Fatal(err)
		}
		defer m.Tailnet("bravo").setEnabled(ctx, true)
		if got, err := read("1.2.3.4:80"); err == nil {
			t.Fatalf("got %q; want an error, not a direct connection", got)
		}
		if c, tgt, err := m.Dial(ctx, "tcp", "1.2.3.4:80"); err == nil {
			c.Close()
			t.Fatalf("proxy dial went %s; want an error", tgt)
		}
		if tgt, err := m.Resolve(ctx, "example.org"); err == nil {
			t.Fatalf("resolved %v; want an error, not the local resolver", tgt)
		}
	})

	t.Run("off", func(t *testing.T) {
		if _, err := m.SetExitNode(ctx, "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := read("1.2.3.4:80"); err == nil {
			t.Fatal("exit node off: a TUN dial to the internet should fail")
		}
		if m.Status().ExitNode != nil {
			t.Fatal("status still shows an exit node")
		}
	})
}

func TestExitNodeOrder(t *testing.T) {
	loc := func(country, city string, prio int) Location {
		return Location{Country: country, City: city, Priority: prio}
	}
	m := New(&Config{Tailnets: []TailnetConfig{{Name: "home"}}}, Options{})
	tn := m.Tailnet("home")
	tn.snap = Snapshot{Name: "home", Running: true, Peers: []Peer{
		{Name: "se-sto", FQDN: "se-sto.mullvad.ts.net", Exit: true, Location: loc("Sweden", "Stockholm", 10)},
		{Name: "nas", FQDN: "nas.tail1.ts.net", Exit: true},
		{Name: "us-nyc-2", FQDN: "us-nyc-2.mullvad.ts.net", Exit: true, Location: loc("USA", "New York", 50)},
		{Name: "us-nyc-1", FQDN: "us-nyc-1.mullvad.ts.net", Exit: true, Location: loc("USA", "New York", 90)},
		{Name: "laptop", FQDN: "laptop.tail1.ts.net"},
	}}
	var got []string
	for _, e := range m.ExitNodes() {
		got = append(got, fmt.Sprintf("%s:%v", e.Name, e.Mullvad))
	}
	want := "nas:false se-sto:true us-nyc-1:true us-nyc-2:true"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}
