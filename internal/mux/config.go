package mux

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	StateDir string `json:"state_dir"` // default ~/.local/share/tailmux
	Hostname string `json:"hostname"`  // device name in every tailnet; default "tailmux-<host>"

	SOCKS5 string `json:"socks5"` // default 127.0.0.1:1055
	HTTP   string `json:"http"`   // HTTP proxy + API + PAC; default 127.0.0.1:1056
	DNS    string `json:"dns"`    // optional DNS server, e.g. 127.0.0.1:1053

	// Direct lets destinations no tailnet claims go out over the normal
	// network, so the proxy can be set system-wide. Default true.
	Direct *bool `json:"direct"`

	Tailnets []TailnetConfig `json:"tailnets"`

	// Pins maps a CIDR or a domain suffix to a tailnet name, for
	// destinations several tailnets claim (overlapping 10.0.0.0/8s).
	Pins map[string]string `json:"pins"`

	TUN TUNConfig `json:"tun"`
}

// TUNConfig turns on proxy-free mode: a virtual interface that carries
// tailnet traffic for every app. Needs root.
type TUNConfig struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`       // default: next free utunN (macOS), "tailmux0" (Linux)
	MTU     int    `json:"mtu"`        // default 1500
	Range   string `json:"fake_range"` // fake IPs handed out for tailnet names; default 198.18.0.0/15
	CGNAT   bool   `json:"cgnat"`      // also route 100.64.0.0/10; off by default so the official client keeps it
	// NoDNS leaves the OS resolver alone; names then only work through
	// the proxies.
	NoDNS bool `json:"no_dns"`
}

// DefaultStateDir can be baked in at build time; the Homebrew formula
// points it into its prefix.
var DefaultStateDir = ""

type TailnetConfig struct {
	Name       string `json:"name"`        // short alias, used as host.<name>
	AuthKey    string `json:"auth_key"`    // literal, or "env:VAR"; empty prints a login URL
	ControlURL string `json:"control_url"` // empty for Tailscale SaaS; set for Headscale
	Hostname   string `json:"hostname"`    // overrides Config.Hostname
	Ephemeral  bool   `json:"ephemeral"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, c.Normalize()
}

// Normalize fills defaults and validates. LoadConfig calls it.
func (c *Config) Normalize() error {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("TAILMUX_STATE_DIR"); v != "" {
		c.StateDir = v
	}
	if c.StateDir == "" && DefaultStateDir != "" {
		c.StateDir = DefaultStateDir
	}
	if c.StateDir == "" {
		c.StateDir = filepath.Join(home, ".local", "share", "tailmux")
	} else if strings.HasPrefix(c.StateDir, "~/") {
		c.StateDir = filepath.Join(home, c.StateDir[2:])
	}
	if c.Hostname == "" {
		h, _ := os.Hostname()
		h = strings.ToLower(strings.TrimSuffix(h, ".local"))
		c.Hostname = "tailmux-" + h
	}
	if c.SOCKS5 == "" {
		c.SOCKS5 = "127.0.0.1:1055"
	}
	if c.HTTP == "" {
		c.HTTP = "127.0.0.1:1056"
	}
	if c.Direct == nil {
		t := true
		c.Direct = &t
	}
	if c.TUN.MTU == 0 {
		c.TUN.MTU = 1500
	}
	if c.TUN.Range == "" {
		c.TUN.Range = "198.18.0.0/15"
	}
	if p, err := netip.ParsePrefix(c.TUN.Range); err != nil || !p.Addr().Is4() || p.Bits() > 24 {
		return fmt.Errorf("config: tun.fake_range %q must be an IPv4 prefix of /24 or larger", c.TUN.Range)
	}
	if len(c.Tailnets) == 0 {
		return fmt.Errorf("config: no tailnets")
	}
	seen := map[string]bool{}
	for i := range c.Tailnets {
		t := &c.Tailnets[i]
		t.Name = strings.ToLower(t.Name)
		if t.Name == "" || strings.ContainsAny(t.Name, ". /") {
			return fmt.Errorf("config: tailnet %d: name must be a single DNS label", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("config: duplicate tailnet %q", t.Name)
		}
		seen[t.Name] = true
		if v, ok := strings.CutPrefix(t.AuthKey, "env:"); ok {
			t.AuthKey = os.Getenv(v)
		}
		if t.Hostname == "" {
			t.Hostname = c.Hostname
		}
	}
	for k, v := range c.Pins {
		if !seen[strings.ToLower(v)] {
			return fmt.Errorf("config: pin %q -> unknown tailnet %q", k, v)
		}
	}
	return nil
}

func (c *Config) pins() Pins {
	p := Pins{Prefixes: map[netip.Prefix]string{}, Domains: map[string]string{}}
	for k, v := range c.Pins {
		v = strings.ToLower(v)
		if pfx, err := netip.ParsePrefix(k); err == nil {
			p.Prefixes[pfx.Masked()] = v
		} else if ip, err := netip.ParseAddr(k); err == nil {
			p.Prefixes[netip.PrefixFrom(ip, ip.BitLen())] = v
		} else {
			p.Domains[strings.TrimPrefix(k, "*.")] = v
		}
	}
	return p
}
