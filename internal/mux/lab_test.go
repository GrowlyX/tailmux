package mux

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/GrowlyX/tailmux/internal/lab"
	"tailscale.com/net/netns"
)

// TestLab keeps three fake tailnets running so the real binary can be
// exercised by hand:
//
//	TAILMUX_LAB=/tmp/lab.json go test ./internal/mux -run TestLab -timeout 0
//	tailmux up -config /tmp/lab.json
func TestLab(t *testing.T) {
	out := os.Getenv("TAILMUX_LAB")
	if out == "" {
		t.Skip("set TAILMUX_LAB=<config path> to run")
	}
	netns.SetEnabled(false)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	alpha, bravo, charlie, webIPs := lab.Start(t, ctx)

	cfg := map[string]any{
		"state_dir": t.TempDir(),
		"hostname":  "tailmux",
		"socks5":    "127.0.0.1:21055",
		"http":      "127.0.0.1:21056",
		"dns":       "127.0.0.1:21053",
		"tailnets": []map[string]any{
			{"name": "alpha", "control_url": alpha.URL, "ephemeral": true},
			{"name": "bravo", "control_url": bravo.URL, "ephemeral": true},
			{"name": "charlie", "control_url": charlie.URL, "ephemeral": true},
		},
		"pins": map[string]string{"192.0.2.128/25": "bravo"},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("lab up; web IPs %v; config at %s", webIPs, out)
	os.WriteFile(out+".ready", nil, 0o644)
	select {
	case <-ctx.Done():
	case <-time.After(time.Hour):
	}
}
