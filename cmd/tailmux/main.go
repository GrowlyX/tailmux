// Command tailmux joins several tailnets at once and routes each
// connection to the tailnet that owns its destination.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/net/proxy"

	"github.com/GrowlyX/tailmux/internal/mux"
	"github.com/GrowlyX/tailmux/internal/setup"
	"github.com/GrowlyX/tailmux/internal/tun"
	"github.com/GrowlyX/tailmux/internal/update"
)

// Set at build time: -X main.version=... -X main.defaultConfig=...
var (
	version       = "dev"
	defaultConfig = ""
)

const usage = `tailmux: be on N tailnets at once.

usage:
  tailmux up [-v] [-tun]       run the daemon (SOCKS5, HTTP proxy, PAC, DNS);
                               -tun also routes tailnets for every app (root)
  tailmux setup                add tailnets and log in (interactive)
  tailmux status               tailnets, routes and conflicts
  tailmux peers [tailnet]      every device, and the name to reach it by
  tailmux resolve <host>       which tailnet a host routes through, and why
  tailmux nc <host> <port>     pipe stdio to host:port (ssh ProxyCommand)
  tailmux repair               re-apply routes and DNS, flush the DNS cache
  tailmux update [-check]      install the latest release (or just check)
  tailmux bar                  open the menu bar app (macOS)
  tailmux example-config       print a starter config
  tailmux version

flags:
  -config path   ($TAILMUX_CONFIG, else ~/.config/tailmux/config.json)
`

func main() {
	log.SetFlags(log.Ltime)
	fs := flag.NewFlagSet("tailmux", flag.ExitOnError)
	cfgPath := fs.String("config", configPath(), "config file")
	verbose := fs.Bool("v", false, "verbose tailscale logs")
	tunFlag := fs.Bool("tun", false, "route tailnet traffic for every app through a TUN device (needs root)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	args := os.Args[1:]
	cmd := "up"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs.Parse(args)

	mux.Version = version
	switch cmd {
	case "example-config":
		fmt.Print(exampleConfig)
		return
	case "version", "-version", "--version":
		fmt.Println("tailmux", version)
		return
	case "setup":
		if err := setup.Run(*cfgPath); err != nil {
			log.Fatal(err)
		}
		return
	case "bar":
		if err := openBar(); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := mux.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("%v\n(run `tailmux setup` to create it)", err)
	}

	switch cmd {
	case "up":
		if *tunFlag {
			cfg.TUN.Enabled = true
		}
		err = up(cfg, *verbose)
	case "status":
		err = status(cfg)
	case "repair":
		err = repair(cfg)
	case "update":
		err = runUpdate(cfg, fs.Args())
	case "peers":
		err = peers(cfg, fs.Arg(0))
	case "resolve":
		if fs.NArg() != 1 {
			fs.Usage()
			os.Exit(2)
		}
		err = resolve(cfg, fs.Arg(0))
	case "nc":
		if fs.NArg() != 2 {
			fs.Usage()
			os.Exit(2)
		}
		err = nc(cfg, fs.Arg(0), fs.Arg(1))
	default:
		fs.Usage()
		os.Exit(2)
	}
	var re errRestart
	if errors.As(err, &re) {
		err = update.Reexec(re.path)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// openBar launches TailmuxBar.app: the copy installed next to this
// binary (Homebrew puts it in the formula prefix), else /Applications.
func openBar() error {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			cands = append(cands, filepath.Join(filepath.Dir(exe), "..", "TailmuxBar.app"))
		}
	}
	cands = append(cands, "/Applications/TailmuxBar.app")
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return exec.Command("open", c).Run()
		}
	}
	return fmt.Errorf("TailmuxBar.app not found (looked in %s); build it with macos/build-app.sh", strings.Join(cands, ", "))
}

// configPath picks $TAILMUX_CONFIG, then the path baked in at build time
// (Homebrew's etc/, shared by the CLI and the service), then
// ~/.config/tailmux/config.json.
func configPath() string {
	if p := os.Getenv("TAILMUX_CONFIG"); p != "" {
		return p
	}
	if defaultConfig != "" {
		return defaultConfig
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tailmux", "config.json")
}

func up(cfg *mux.Config, verbose bool) error {
	if cfg.TUN.Enabled && os.Geteuid() != 0 {
		return fmt.Errorf("tun mode needs root: sudo tailmux up -tun (or `sudo brew services start tailmux`)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := mux.New(cfg, mux.Options{Verbose: verbose})
	defer m.Close()

	socksLn, err := net.Listen("tcp", cfg.SOCKS5)
	if err != nil {
		return err
	}
	httpLn, err := net.Listen("tcp", cfg.HTTP)
	if err != nil {
		return err
	}
	go m.ServeSOCKS5(socksLn)
	go (&http.Server{Handler: m.HTTPHandler(cfg.SOCKS5)}).Serve(httpLn)
	if cfg.DNS != "" {
		pc, err := net.ListenPacket("udp", cfg.DNS)
		if err != nil {
			return err
		}
		go m.ServeDNS(pc)
		log.Printf("dns      %s", cfg.DNS)
	}
	log.Printf("socks5   %s", cfg.SOCKS5)
	log.Printf("http     %s  (proxy, /status, /resolve, /proxy.pac)", cfg.HTTP)

	if err := m.Start(ctx); err != nil {
		return err
	}
	if cfg.TUN.Enabled {
		sys, err := tun.Start(ctx, m, cfg)
		if err != nil {
			return err
		}
		defer sys.Close()
		m.Repair = func() { sys.Repair("requested") }
		m.TUNStatus = func() any {
			return map[string]any{"interface": sys.Interface(), "fake_range": sys.FakeIPs().Prefix().String(), "dns": sys.DNSActive()}
		}
	}
	names := make([]string, 0, len(cfg.Tailnets))
	for _, t := range cfg.Tailnets {
		names = append(names, t.Name)
	}
	log.Printf("joining %d tailnets: %s", len(names), strings.Join(names, ", "))

	// Updates: check (and by default install) new releases; when the
	// binary we were started as changes, shut down cleanly and re-exec.
	restart := make(chan struct{})
	invoked := update.InvokedPath()
	if exe, err := filepath.EvalSymlinks(invoked); err == nil {
		if err := update.CleanupOldKegs(exe, func(f string, a ...any) { log.Printf("update: "+f, a...) }); err != nil {
			log.Printf("update: %v", err)
		}
	}
	if cfg.Updates.CheckEnabled() {
		um := update.NewManager(version, cfg.Updates.AutoEnabled())
		m.UpdateStatus = func() any { return um.Status() }
		m.TriggerUpdate = func() error {
			go func() {
				if err := um.Install(context.Background()); err != nil {
					log.Printf("update: %v", err)
				}
			}()
			return nil
		}
		go um.Run(ctx)
	}
	go update.WatchExecutable(ctx, invoked, 15*time.Second, func() { close(restart) })

	select {
	case <-ctx.Done():
		log.Printf("shutting down")
		return nil
	case <-restart:
		log.Printf("new tailmux binary installed; restarting")
		return errRestart{invoked}
	}
}

// errRestart makes main re-exec after up's deferred cleanup (TUN device,
// resolver files, tailnet nodes) has run.
type errRestart struct{ path string }

func (e errRestart) Error() string { return "restart into " + e.path }

func apiGet(cfg *mux.Config, path string, v any) error {
	c := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Get("http://" + cfg.HTTP + path)
	if err != nil {
		return fmt.Errorf("is `tailmux up` running? %w", err)
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func status(cfg *mux.Config) error {
	var s mux.Status
	if err := apiGet(cfg, "/status", &s); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TAILNET\tSTATE\tSUFFIX\tSELF\tPEERS")
	for _, t := range s.Tailnets {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d/%d online\n", t.Name, t.State, t.Suffix, strings.Join(t.SelfIPs, ","), t.Online, t.Peers)
	}
	tw.Flush()
	if u, ok := s.Update.(map[string]any); ok && u["available"] == true {
		fmt.Printf("\nupdate: tailmux %v is available (running %v); `tailmux update` or wait for auto-update\n", u["latest"], s.Version)
	}
	if t, ok := s.TUN.(map[string]any); ok {
		fmt.Printf("\ntun: %v, fake IPs %v, OS DNS %v\n", t["interface"], t["fake_range"], t["dns"])
	}
	for _, t := range s.Tailnets {
		if t.AuthURL != "" {
			fmt.Printf("\n%s needs login: %s\n", t.Name, t.AuthURL)
		}
		if len(t.Routes) > 0 || len(t.SplitDNS) > 0 {
			fmt.Printf("\n%s:\n", t.Name)
			for _, r := range t.Routes {
				fmt.Printf("  route  %s\n", r)
			}
			for _, d := range t.SplitDNS {
				fmt.Printf("  dns    %s\n", d)
			}
		}
	}
	if len(s.Conflicts) > 0 {
		fmt.Println("\nconflicts (claimed by several tailnets):")
		for _, c := range s.Conflicts {
			how := "priority"
			if c.Pinned {
				how = "pinned"
			}
			fmt.Printf("  %-20s %s -> %s (%s)\n", c.What, strings.Join(c.Tailnets, ","), c.Winner, how)
		}
	}
	return nil
}

func repair(cfg *mux.Config) error {
	req, _ := http.NewRequest("POST", "http://"+cfg.HTTP+"/repair", nil)
	req.Header.Set("X-Tailmux", "1")
	resp, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return fmt.Errorf("is `tailmux up` running? %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	fmt.Println("routes and DNS re-applied, DNS cache flushed")
	return nil
}

// runUpdate updates this install in place. A running daemon notices its
// binary changed and restarts onto the new one by itself.
func runUpdate(cfg *mux.Config, args []string) error {
	ctx := context.Background()
	rel, err := update.Latest(ctx)
	if err != nil {
		return err
	}
	if !update.Newer(version, rel.Version()) {
		fmt.Printf("tailmux %s is up to date (latest %s)\n", version, rel.Version())
		return nil
	}
	fmt.Printf("tailmux %s is available (you have %s): %s\n", rel.Version(), version, rel.URL)
	if len(args) > 0 && (args[0] == "-check" || args[0] == "--check") {
		return nil
	}
	um := update.NewManager(version, false)
	if err := update.Install(ctx, rel, um.Exe, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }); err != nil {
		return err
	}
	fmt.Println("done; a running daemon restarts onto it within a few seconds")
	return nil
}

func peers(cfg *mux.Config, only string) error {
	var ps []mux.PeerInfo
	if err := apiGet(cfg, "/peers", &ps); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tADDRESS\tSTATUS\tROUTES")
	for _, p := range ps {
		if only != "" && p.Tailnet != only {
			continue
		}
		st := "offline"
		if p.Online {
			st = "online"
		}
		addr := ""
		if len(p.IPs) > 0 {
			addr = p.IPs[0]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Alias, addr, st, strings.Join(p.Routes, " "))
	}
	return tw.Flush()
}

func resolve(cfg *mux.Config, host string) error {
	var r struct {
		Target mux.Target `json:"target"`
		Error  string     `json:"error"`
	}
	if err := apiGet(cfg, "/resolve?host="+url.QueryEscape(host), &r); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return nil
}

func nc(cfg *mux.Config, host, port string) error {
	d, err := proxy.SOCKS5("tcp", cfg.SOCKS5, nil, proxy.Direct)
	if err != nil {
		return err
	}
	c, err := d.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer c.Close()
	done := make(chan struct{})
	go func() {
		io.Copy(c, os.Stdin)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	go func() {
		io.Copy(os.Stdout, c)
		close(done)
	}()
	<-done
	return nil
}

const exampleConfig = `{
  "hostname": "tailmux-laptop",
  "socks5": "127.0.0.1:1055",
  "http": "127.0.0.1:1056",
  "direct": true,
  "tailnets": [
    { "name": "work" },
    { "name": "home", "auth_key": "env:TS_HOME_AUTHKEY" },
    { "name": "lab", "control_url": "https://headscale.example.com" }
  ],
  "pins": {
    "10.0.0.0/24": "home",
    "corp.internal": "work"
  },
  "tun": { "enabled": false }
}
`
