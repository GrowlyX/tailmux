package mux

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GrowlyX/tailmux/internal/lab"
	"tailscale.com/net/netns"
)

// TestLoginsFollowTailnets runs real logins against two tailnets whose
// control servers want a browser login, then: swaps the tailnets' names
// (each must keep its own login, with no new login), logs one out (it
// must ask for a new login), and removes one and adds it back (the old
// login must be gone).
func TestLoginsFollowTailnets(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up two tailnets")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// The names in the config start out the wrong way round, as in the
	// bug report: "home" is the skyblock tailnet and vice versa.
	skyblock, home := lab.NewTailnet(t, "skyblock"), lab.NewTailnet(t, "home")
	skyblock.Control.RequireAuth = true
	home.Control.RequireAuth = true
	controls := map[string]*lab.Tailnet{skyblock.URL: skyblock, home.URL: home}

	dir := t.TempDir()
	t.Setenv("TAILMUX_STATE_DIR", dir)
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, fmt.Appendf(nil, `{"hostname":"tailmux","socks5":"127.0.0.1:0","http":"127.0.0.1:0",
		"tailnets":[{"name":"home","control_url":%q},{"name":"skyblock","control_url":%q}]}`, skyblock.URL, home.URL), 0o600)

	start := func() *Mux {
		t.Helper()
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		m := New(cfg, Options{})
		m.ConfigPath = path
		if err := m.PinStates(); err != nil {
			t.Fatal(err)
		}
		if err := m.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// login completes each pending browser login and waits for the named
	// tailnets to run. It returns each one's address and MagicDNS suffix.
	login := func(m *Mux, names ...string) map[string]string {
		t.Helper()
		got := map[string]string{}
		lab.Eventually(t, "logged in: "+strings.Join(names, ", "), 90*time.Second, func() error {
			for _, n := range names {
				tn := m.get(n)
				if tn == nil {
					return fmt.Errorf("%s: no such tailnet", n)
				}
				st := tn.Status()
				if st.AuthURL != "" {
					controls[tn.cfg.ControlURL].Control.CompleteAuth(st.AuthURL)
				}
				if st.State != "Running" || len(st.SelfIPs) == 0 || st.Suffix == "" {
					return fmt.Errorf("%s: %s", n, st.State)
				}
				got[n] = st.Suffix + " " + st.SelfIPs[0]
			}
			return nil
		})
		return got
	}
	// noLogin fails if a tailnet asks for a login while coming up.
	noLogin := func(m *Mux, names ...string) map[string]string {
		t.Helper()
		got := map[string]string{}
		lab.Eventually(t, "back without a login: "+strings.Join(names, ", "), 90*time.Second, func() error {
			for _, n := range names {
				st := m.get(n).Status()
				if st.AuthURL != "" {
					t.Fatalf("%s asked for a new login: %s", n, st.AuthURL)
				}
				if st.State != "Running" || len(st.SelfIPs) == 0 || st.Suffix == "" {
					return fmt.Errorf("%s: %s", n, st.State)
				}
				got[n] = st.Suffix + " " + st.SelfIPs[0]
			}
			return nil
		})
		return got
	}

	m := start()
	before := login(m, "home", "skyblock")
	if !strings.HasPrefix(before["home"], "skyblock.") || !strings.HasPrefix(before["skyblock"], "home.") {
		t.Fatalf("setup: %v", before)
	}
	m.Close()

	t.Run("swap names", func(t *testing.T) {
		// What `tailmux setup` does for a swap: three renames, one save.
		c, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Tailnets[0].Name, c.Tailnets[1].Name = "skyblock", "home"
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		m = start()
		after := noLogin(m, "home", "skyblock")
		if after["home"] != before["skyblock"] || after["skyblock"] != before["home"] {
			t.Fatalf("logins didn't follow the rename:\nbefore %v\nafter  %v", before, after)
		}
	})

	srv := httptest.NewServer(m.HTTPHandler("127.0.0.1:0"))
	t.Cleanup(srv.Close)
	call := func(method, path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, nil)
		req.Header.Set("X-Tailmux", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	t.Run("log out", func(t *testing.T) {
		was := m.get("home").Status()
		code, out := call("POST", "/tailnets/home/logout")
		if code != 200 {
			t.Fatalf("logout: %d %s", code, out)
		}
		lab.Eventually(t, "a new login URL", 30*time.Second, func() error {
			st := m.get("home").Status()
			if st.AuthURL == "" {
				return fmt.Errorf("state %s, no login URL", st.State)
			}
			if !strings.HasPrefix(st.AuthURL, home.URL) {
				t.Fatalf("login went to another control server: %s", st.AuthURL)
			}
			return nil
		})
		now := login(m, "home")
		if now["home"] == was.Suffix+" "+was.SelfIPs[0] {
			t.Fatalf("still the old device after logging out: %s", now["home"])
		}
		if !strings.HasPrefix(now["home"], "home.") {
			t.Fatalf("logged back into the wrong tailnet: %s", now["home"])
		}
		noLogin(m, "skyblock") // the other tailnet is untouched
		if code, _ := call("POST", "/tailnets/nope/logout"); code != 404 {
			t.Fatalf("unknown tailnet: %d", code)
		}
	})

	t.Run("remove forgets the login", func(t *testing.T) {
		old := m.get("skyblock").srv.Dir
		if code, out := call("DELETE", "/tailnets/skyblock"); code != 204 {
			t.Fatalf("remove: %d %s", code, out)
		}
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Fatalf("login left behind in %s: %v", old, err)
		}
		if _, err := m.AddTailnet(TailnetConfig{Name: "skyblock", ControlURL: skyblock.URL}); err != nil {
			t.Fatal(err)
		}
		lab.Eventually(t, "re-added tailnet asks for a login", 30*time.Second, func() error {
			if st := m.get("skyblock").Status(); st.AuthURL == "" {
				return fmt.Errorf("state %s, no login URL", st.State)
			}
			return nil
		})
		login(m, "skyblock")
		c, _ := ReadConfig(path)
		states := map[string]string{}
		for _, tn := range c.Tailnets {
			states[tn.Name] = tn.State
		}
		if states["home"] != "skyblock" || states["skyblock"] == "" || states["skyblock"] == "home" {
			t.Fatalf("saved states: %v", states)
		}
	})
	m.Close()
}
