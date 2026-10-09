package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tailscale.com/net/netns"

	"github.com/GrowlyX/tailmux/internal/lab"
	"github.com/GrowlyX/tailmux/internal/mux"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

func TestListEditing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	m := newModel(path, &mux.Config{Tailnets: []mux.TailnetConfig{{Name: "a"}, {Name: "b"}, {Name: "c"}}})

	press(m, "j", "J") // move b below c
	if got := names(m.cfg); got != "a,c,b" {
		t.Fatalf("reorder: %s", got)
	}
	press(m, "k", "d") // delete c
	if got := names(m.cfg); got != "a,b" {
		t.Fatalf("delete: %s", got)
	}
	press(m, "t")
	if !m.cfg.TUN.Enabled || !m.dirty {
		t.Fatal("t should enable TUN and mark dirty")
	}
	press(m, "q")
	if m.screen != screenList || !m.quitOK {
		t.Fatal("first q with unsaved changes should warn, not quit")
	}
	press(m, "s")
	if m.dirty {
		t.Fatalf("save: %s", m.note)
	}
	back, err := mux.ReadConfig(path)
	if err != nil || names(back) != "a,b" || !back.TUN.Enabled {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if v := m.View().Content; !strings.Contains(v, "TUN mode") || !strings.Contains(v, "a add") {
		t.Fatalf("view:\n%s", v)
	}
	press(m, "a")
	if m.screen != screenForm {
		t.Fatal("a should open the add form")
	}
	press(m, "esc")
	if m.screen != screenList {
		t.Fatal("esc should close the form")
	}
}

func names(c *mux.Config) string {
	var n []string
	for _, t := range c.Tailnets {
		n = append(n, t.Name)
	}
	return strings.Join(n, ",")
}

func TestParsePins(t *testing.T) {
	got, err := parsePins("10.0.0.0/24 = Home\n# comment\n\ncorp.internal=work\n", []string{"home", "work"})
	if err != nil || got["10.0.0.0/24"] != "home" || got["corp.internal"] != "work" || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := parsePins("10.0.0.0/24 = nope", []string{"home"}); err == nil {
		t.Fatal("unknown tailnet should fail")
	}
	if _, err := parsePins("garbage", []string{"home"}); err == nil {
		t.Fatal("missing = should fail")
	}
}

// TestLogin drives the login screen against real (in-process) tailnets:
// with no daemon running, setup brings the tailnets up itself.
func TestLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up tailnets")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	alpha, bravo, _, _ := lab.Start(t, ctx)

	cfg := &mux.Config{
		StateDir: t.TempDir(),
		HTTP:     "127.0.0.1:1", // nothing listens: forces in-process login
		Tailnets: []mux.TailnetConfig{
			{Name: "alpha", ControlURL: alpha.URL, Ephemeral: true},
			{Name: "bravo", ControlURL: bravo.URL, Ephemeral: true},
		},
	}
	m := newModel(filepath.Join(t.TempDir(), "c.json"), cfg)
	_, cmd := m.Update(key("l"))
	if m.screen != screenLogin || cmd == nil {
		t.Fatal("l should start login")
	}
	m.Update(cmd()) // loginStartedMsg
	defer m.login.stop()
	lab.Eventually(t, "both tailnets connected", 60*time.Second, func() error {
		m.Update(tickMsg(time.Now()))
		v := m.View().Content
		if !strings.Contains(v, "All 2 tailnets connected") {
			return fmt.Errorf("not yet:\n%s", v)
		}
		return nil
	})

	// x logs the selected tailnet out; it comes back as a new device.
	ip := func() string {
		if st := m.login.statuses[0]; st.State == "Running" && len(st.SelfIPs) > 0 {
			return st.SelfIPs[0]
		}
		return ""
	}
	was := ip()
	_, cmd = m.Update(key("x"))
	if cmd == nil {
		t.Fatal("x should log out")
	}
	m.Update(cmd()) // loggedOutMsg
	if m.login.err != "" {
		t.Fatalf("log out: %s", m.login.err)
	}
	lab.Eventually(t, "alpha back as a new device", 60*time.Second, func() error {
		m.Update(tickMsg(time.Now()))
		if now := ip(); now == "" || now == was {
			return fmt.Errorf("alpha: %s (was %s)", now, was)
		}
		return nil
	})

	press(m, "esc")
	if m.screen != screenList || !strings.Contains(m.note, "2 of 2") {
		t.Fatalf("after login: screen=%v note=%q", m.screen, m.note)
	}
}
