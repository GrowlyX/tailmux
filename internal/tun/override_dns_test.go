package tun

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/net/netns"

	"github.com/GrowlyX/tailmux/internal/lab"
	"github.com/GrowlyX/tailmux/internal/mux"
)

// TestOverrideLocalDNS sends every name, of every type, to the tailnet's
// global nameserver, as the official client does.
func TestOverrideLocalDNS(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up a tailnet")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	answer := netip.MustParseAddr("203.0.113.53")
	f := lab.NewTailnet(t, "override")
	const zone = "corp.internal"
	f.GlobalDNS(t, ctx, zone, answer)
	cfg := &mux.Config{StateDir: t.TempDir(), Hostname: "tailmux", Tailnets: []mux.TailnetConfig{{Name: "override", ControlURL: f.URL, Ephemeral: true}}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := mux.New(cfg, mux.Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	e := &Engine{m: m, fake: NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), "")}

	const id = 42
	ask := func(name string, qt dnsmessage.Type) ([]dnsmessage.Resource, error) {
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
		b.StartQuestions()
		b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: qt, Class: dnsmessage.ClassINET})
		q, _ := b.Finish()
		var p dnsmessage.Parser
		h, err := p.Start(e.answerDNS(q, "udp"))
		if err != nil || h.ID != id {
			return nil, fmt.Errorf("%v: id %d, %v", qt, h.ID, err)
		}
		p.SkipAllQuestions()
		as, _ := p.AllAnswers()
		if len(as) != 1 || as[0].Header.Type != qt {
			return nil, fmt.Errorf("%v: %v, answers %v", qt, h.RCode, as)
		}
		return as, nil
	}
	lab.Eventually(t, "the tailnet's nameserver answers every name", 90*time.Second, func() error {
		as, err := ask("db.corp.internal.", dnsmessage.TypeA)
		if err != nil {
			return err
		}
		if a := as[0].Body.(*dnsmessage.AResource).A; a != answer.As4() {
			return fmt.Errorf("A %v, want %v", a, answer)
		}
		return nil
	})
	txt := func(name string) string {
		t.Helper()
		as, err := ask(name, dnsmessage.TypeTXT)
		if err != nil {
			t.Fatal(err)
		}
		return as[0].Body.(*dnsmessage.TXTResource).TXT[0]
	}
	if got := txt("db.corp.internal."); got != zone {
		t.Errorf("TXT %q, want the tailnet nameserver's", got)
	}
	// Local names stay on the local network, whatever the record type.
	const lanTXT = "lan"
	lan := serveTXT(t, lanTXT)
	m.SetLocalDNS(func() []netip.AddrPort { return []netip.AddrPort{lan} })
	if got := txt("printer.home.arpa."); got != lanTXT {
		t.Errorf("local name's TXT %q, want the local nameserver's", got)
	}
}

// serveTXT answers every query on a local UDP port with one TXT record.
func serveTXT(t *testing.T, txt string) netip.AddrPort {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
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
			q, _ := p.Question()
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true})
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			b.TXTResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET}, dnsmessage.TXTResource{TXT: []string{txt}})
			out, _ := b.Finish()
			pc.WriteTo(out, addr)
		}
	}()
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}
