package mux

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GrowlyX/tailmux/internal/lab"
	"tailscale.com/net/netns"
)

func TestTailnetLock(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up a locked tailnet")
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	locked := lab.NewTailnet(t, "locked")
	locked.AllowLock()
	locked.WebNode(t, ctx)
	locked.Lock(t, ctx) // signs web; tailmux joins after, unsigned

	cfg := &Config{
		StateDir: t.TempDir(),
		Hostname: "tailmux",
		Tailnets: []TailnetConfig{{Name: "locked", ControlURL: locked.URL, Ephemeral: true}},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, Options{MemStore: true})
	t.Cleanup(func() { m.Close() })
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	tn := m.Tailnet("locked")

	var lock *LockStatus
	lab.Eventually(t, "tailmux sees it needs a signature", 90*time.Second, func() error {
		lock = tn.Status().Lock
		if !lock.needsSignature() || lock.NodeKey == "" || lock.PublicKey == "" {
			return fmt.Errorf("lock: %+v (state %s)", lock, tn.Status().State)
		}
		return nil
	})
	if want := "tailscale lock sign " + lock.NodeKey + " " + lock.PublicKey; lock.SignCommand != want {
		t.Errorf("sign command %q, want %q", lock.SignCommand, want)
	}
	if !strings.HasPrefix(lock.NodeKey, "nodekey:") || !strings.HasPrefix(lock.PublicKey, "tlpub:") {
		t.Errorf("keys not in CLI form: %q %q", lock.NodeKey, lock.PublicKey)
	}
	// Unsigned, the tailnet claims nothing: its devices would drop us.
	if s := tn.Snapshot(); s.Running || s.Reconnecting {
		t.Errorf("unsigned tailnet is active: %+v", s)
	}
	if _, domains := m.Router().Claims(); len(domains) > 0 {
		t.Errorf("unsigned tailnet claims DNS domains %v", domains)
	}
	if addrs := m.Router().PeerAddrs(); len(addrs) > 0 {
		t.Errorf("unsigned tailnet claims device addresses %v", addrs)
	}

	// GET /lock reports the same.
	rec := httptest.NewRecorder()
	m.HTTPHandler("127.0.0.1:1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/lock", nil))
	if !strings.Contains(rec.Body.String(), lock.NodeKey) {
		t.Errorf("/lock: %s", rec.Body)
	}

	// A signing device runs the command; tailmux comes up.
	if err := locked.Sign(ctx, lock.NodeKey, lock.PublicKey); err != nil {
		t.Fatal(err)
	}
	lab.Eventually(t, "signed and reaching web.locked", 90*time.Second, func() error {
		if l := tn.Status().Lock; l == nil || !l.Signed {
			return fmt.Errorf("lock: %+v", l)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		c, _, err := m.DialTailnet(ctx, "tcp", "web.locked:80")
		if err != nil {
			return err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(c, "GET / HTTP/1.0\r\nHost: web\r\n\r\n")
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	})
}
