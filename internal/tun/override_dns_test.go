package tun

import (
	"context"
	"fmt"
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
	f.GlobalDNS(t, ctx, "corp.internal", answer)
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
	ask := func(qt dnsmessage.Type) ([]dnsmessage.Resource, error) {
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
		b.StartQuestions()
		b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("db.corp.internal."), Type: qt, Class: dnsmessage.ClassINET})
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
		as, err := ask(dnsmessage.TypeA)
		if err != nil {
			return err
		}
		if a := as[0].Body.(*dnsmessage.AResource).A; a != answer.As4() {
			return fmt.Errorf("A %v, want %v", a, answer)
		}
		return nil
	})
	if _, err := ask(dnsmessage.TypeTXT); err != nil {
		t.Fatal(err)
	}
}
