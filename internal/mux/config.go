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
	StateDir string `json:"state_dir,omitempty"` // default ~/.local/share/tailmux
	Hostname string `json:"hostname,omitempty"`  // device name in every tailnet; default "tailmux-<host>"

	SOCKS5 string `json:"socks5,omitempty"` // default 127.0.0.1:1055
	HTTP   string `json:"http,omitempty"`   // HTTP proxy + API + PAC; default 127.0.0.1:1056
	DNS    string `json:"dns,omitempty"`    // optional DNS server, e.g. 127.0.0.1:1053

	// Direct lets destinations no tailnet claims go out over the normal
	// network, so the proxy can be set system-wide. Default true.
	Direct *bool `json:"direct,omitempty"`

	Tailnets []TailnetConfig `json:"tailnets"`

	// Pins maps a CIDR or a domain suffix to a tailnet name, for
	// destinations several tailnets claim (overlapping 10.0.0.0/8s).
	Pins map[string]string `json:"pins,omitempty"`

	// ExitNode sends everything no tailnet claims through one exit node,
	// like the official client's exit node menu. Unset: direct.
	ExitNode *ExitNodeConfig `json:"exit_node,omitempty"`

	TUN TUNConfig `json:"tun,omitzero"`

	Updates UpdatesConfig `json:"updates,omitzero"`
}

// UpdatesConfig: by default tailmux checks for releases every few hours
// and installs them, then restarts itself onto the new version.
type UpdatesConfig struct {
	Check *bool `json:"check,omitempty"` // default true
	Auto  *bool `json:"auto,omitempty"`  // default true; false only notifies
}

func (u UpdatesConfig) CheckEnabled() bool { return u.Check == nil || *u.Check }
func (u UpdatesConfig) AutoEnabled() bool  { return u.CheckEnabled() && (u.Auto == nil || *u.Auto) }

// TUNConfig turns on proxy-free mode: a virtual interface that carries
// tailnet traffic for every app. Needs root.
type TUNConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Name    string `json:"name,omitempty"`       // default: next free utunN (macOS), "tailmux0" (Linux)
	MTU     int    `json:"mtu,omitempty"`        // default 1500
	Range   string `json:"fake_range,omitempty"` // fake IPs handed out for tailnet names; default 198.18.0.0/15
	CGNAT   bool   `json:"cgnat,omitempty"`      // also route 100.64.0.0/10; off by default so the official client keeps it
	// PeerRoutes routes each tailnet device's own address (a /32) into the
	// TUN, so names that resolve to one through ordinary DNS work too.
	// Default true.
	PeerRoutes *bool `json:"peer_routes,omitempty"`
	// NoDNS leaves the OS resolver alone; names then only work through
	// the proxies.
	NoDNS bool `json:"no_dns,omitempty"`
	// RealIPs answers tailnet names with their real addresses when no
	// other tailnet uses the same ones; false always hands out fake IPs.
	// Default true.
	RealIPs *bool `json:"real_ips,omitempty"`
}

// ExitNodeConfig picks an exit node: a device in one of the tailnets
// that offers itself as one, Mullvad nodes included.
type ExitNodeConfig struct {
	Tailnet string `json:"tailnet"`
	// Node is the device's MagicDNS name, short name, Tailscale IP or
	// stable node ID.
	Node string `json:"node"`
}

// DefaultStateDir can be baked in at build time; the Homebrew formula
// points it into its prefix.
var DefaultStateDir = ""

type TailnetConfig struct {
	Name       string `json:"name"`                  // short alias, used as host.<name>
	AuthKey    string `json:"auth_key,omitempty"`    // literal, or "env:VAR"; empty prints a login URL
	ControlURL string `json:"control_url,omitempty"` // empty for Tailscale SaaS; set for Headscale
	Hostname   string `json:"hostname,omitempty"`    // overrides Config.Hostname
	Ephemeral  bool   `json:"ephemeral,omitempty"`
	// State names the directory under state_dir that holds this
	// tailnet's login. It is picked once, when the tailnet is added, and
	// never follows the name: renaming a tailnet keeps its login, and a
	// new tailnet never picks up an old one left under the same name.
	// Configs from before it existed default to the name, which is where
	// those logins live.
	State string `json:"state,omitempty"`
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
	seen, states := map[string]bool{}, map[string]string{}
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
		if t.State == "" {
			t.State = t.Name
		}
		if validName(t.State) != "" {
			return fmt.Errorf("config: tailnet %q: state %q must be a single directory name", t.Name, t.State)
		}
		// Case-blind: on macOS and Windows "Work" and "work" are one folder.
		if other, ok := states[strings.ToLower(t.State)]; ok {
			return fmt.Errorf("config: tailnets %q and %q share the state %q; each needs its own login", other, t.Name, t.State)
		}
		states[strings.ToLower(t.State)] = t.Name
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
	if e := c.ExitNode; e != nil {
		e.Tailnet = strings.ToLower(e.Tailnet)
		switch {
		case e.Tailnet == "" && e.Node == "":
			c.ExitNode = nil
		case !seen[e.Tailnet]:
			return fmt.Errorf("config: exit_node: unknown tailnet %q", e.Tailnet)
		case e.Node == "":
			return fmt.Errorf("config: exit_node: no node")
		}
	}
	return nil
}

// PinStates records each tailnet's state directory in the config, for
// tailnets that predate the state key. Until it is written down, the
// state follows the name, so a rename would strand the login.
func (c *Config) PinStates() {
	for i := range c.Tailnets {
		if c.Tailnets[i].State == "" {
			c.Tailnets[i].State = strings.ToLower(c.Tailnets[i].Name)
		}
	}
}

// NewState picks a state directory for a tailnet being added: the name
// if nothing uses it yet, else the name with a number. A directory that
// already exists is never reused; it holds some other tailnet's login.
func (c *Config) NewState(stateDir, name string) (string, error) {
	taken := func(s string) (bool, error) {
		for _, t := range c.Tailnets {
			if strings.EqualFold(t.State, s) || (t.State == "" && strings.EqualFold(t.Name, s)) {
				return true, nil
			}
		}
		_, err := os.Lstat(filepath.Join(stateDir, s))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			// Say so rather than guess: every name would look taken.
			return true, fmt.Errorf("state directory: %w", err)
		}
		return true, nil
	}
	s := name
	for n := 2; ; n++ {
		used, err := taken(s)
		if err != nil || !used {
			return s, err
		}
		s = fmt.Sprintf("%s-%d", name, n)
	}
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

// Save writes c as indented JSON. The file can hold auth keys, so it is
// private to the user.
func (c *Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	uid, gid, owned := fileOwner(path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if owned {
		// The root daemon saving a user's config must not take it over.
		os.Chown(tmp, uid, gid)
	}
	return os.Rename(tmp, path)
}

// ReadConfig parses path without filling defaults, for editing. A
// missing file gives an empty config.
func ReadConfig(path string) (*Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}
