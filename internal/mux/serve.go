package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tailscale.com/ipn"
)

// ServeEntry is one `tailscale serve` forward. Exactly one of TCP,
// TLSTCP, HTTP and HTTPS is set.
type ServeEntry struct {
	Port   uint16 `json:"port"`
	TCP    string `json:"tcp,omitempty"`     // port or host:port
	TLSTCP string `json:"tls_tcp,omitempty"` // like tcp, after terminating TLS
	HTTP   string `json:"http,omitempty"`    // port, host:port or URL
	HTTPS  string `json:"https,omitempty"`   // like http, served with the node's certificate
	Path   string `json:"path,omitempty"`    // http and https only; default "/"
}

type serveKind int

const (
	serveNone serveKind = iota
	serveTCP
	serveTLSTCP
	serveHTTP
	serveHTTPS
)

func (k serveKind) web() bool { return k == serveHTTP || k == serveHTTPS }

const (
	tcpScheme        = "tcp"
	httpScheme       = "http"
	defaultServePath = "/"
	serveRecordFile  = "tailmux-serve.json"
)

var (
	tcpSchemes = []string{tcpScheme}
	webSchemes = []string{httpScheme, "https", "https+insecure"}
)

// target points at the field holding the entry's destination for kind k.
func (e *ServeEntry) target(k serveKind) *string {
	switch k {
	case serveTCP:
		return &e.TCP
	case serveTLSTCP:
		return &e.TLSTCP
	case serveHTTP:
		return &e.HTTP
	case serveHTTPS:
		return &e.HTTPS
	}
	return nil
}

func (e *ServeEntry) kind() (serveKind, error) {
	found := serveNone
	for _, k := range []serveKind{serveTCP, serveTLSTCP, serveHTTP, serveHTTPS} {
		if *e.target(k) == "" {
			continue
		}
		if found != serveNone {
			return serveNone, fmt.Errorf("serve port %d: set only one of tcp, tls_tcp, http, https", e.Port)
		}
		found = k
	}
	if found == serveNone {
		return serveNone, fmt.Errorf("serve port %d: set one of tcp, tls_tcp, http, https", e.Port)
	}
	return found, nil
}

type servePath struct {
	port uint16
	path string
}

// normalizeServes validates entries and expands their targets in place
// the way the tailscale CLI does: "3000" becomes "http://127.0.0.1:3000"
// for web entries and "127.0.0.1:3000" for TCP ones.
func normalizeServes(entries []ServeEntry) error {
	kinds := map[uint16]serveKind{}
	paths := map[servePath]bool{}
	for i := range entries {
		e := &entries[i]
		if e.Port == 0 {
			return fmt.Errorf("serve entry %d: no port", i)
		}
		k, err := e.kind()
		if err != nil {
			return err
		}
		if prev, ok := kinds[e.Port]; ok && (prev != k || !k.web()) {
			return fmt.Errorf("serve port %d is listed more than once", e.Port)
		}
		kinds[e.Port] = k
		t := e.target(k)
		if !k.web() {
			if e.Path != "" {
				return fmt.Errorf("serve port %d: path only applies to http and https", e.Port)
			}
			fwd, err := ipn.ExpandProxyTargetValue(*t, tcpSchemes, tcpScheme)
			if err != nil {
				return fmt.Errorf("serve port %d: %w", e.Port, err)
			}
			*t = strings.TrimPrefix(fwd, tcpScheme+"://")
			continue
		}
		if e.Path == "" {
			e.Path = defaultServePath
		}
		if !strings.HasPrefix(e.Path, "/") {
			return fmt.Errorf("serve port %d: path %q must start with /", e.Port, e.Path)
		}
		if paths[servePath{e.Port, e.Path}] {
			return fmt.Errorf("serve port %d: path %q is listed more than once", e.Port, e.Path)
		}
		paths[servePath{e.Port, e.Path}] = true
		proxy, err := ipn.ExpandProxyTargetValue(*t, webSchemes, httpScheme)
		if err != nil {
			return fmt.Errorf("serve port %d: %w", e.Port, err)
		}
		*t = proxy
	}
	return nil
}

// serveRecord is what tailmux last wrote into a node's serve config, so
// entries dropped from the config can be removed without touching ones
// added by hand.
type serveRecord struct {
	TCP []uint16                  `json:"tcp,omitempty"`
	Web map[ipn.HostPort][]string `json:"web,omitempty"` // mounts
}

func (r serveRecord) empty() bool { return len(r.TCP) == 0 && len(r.Web) == 0 }

func loadServeRecord(path string) (serveRecord, error) {
	var r serveRecord
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

func saveServeRecord(path string, r serveRecord) error {
	if r.empty() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// applyServes removes what prev says tailmux added to sc, then adds
// entries for the node named host. Entries win over anything already on
// their ports. It returns the record of what it added.
func applyServes(sc *ipn.ServeConfig, prev serveRecord, entries []ServeEntry, host string) serveRecord {
	for _, port := range prev.TCP {
		if h := sc.TCP[port]; h != nil && h.TCPForward != "" {
			sc.RemoveTCPForwarding("", port)
		}
	}
	for hp, mounts := range prev.Web {
		h, _, err := net.SplitHostPort(string(hp))
		port, perr := hp.Port()
		if err != nil || perr != nil || sc.Web[hp] == nil {
			continue
		}
		sc.RemoveWebHandler(h, port, mounts, true)
	}

	var next serveRecord
	cleared := map[uint16]bool{}
	for _, e := range entries {
		k, err := e.kind()
		if err != nil {
			continue // Normalize rejects these
		}
		target := *e.target(k)
		if !cleared[e.Port] {
			removeWebOnPort(sc, e.Port)
			if h := sc.TCP[e.Port]; h != nil && h.TCPForward != "" {
				sc.RemoveTCPForwarding("", e.Port)
			}
			cleared[e.Port] = true
		}
		if !k.web() {
			sc.SetTCPForwarding(e.Port, target, k == serveTLSTCP, 0, host)
			next.TCP = append(next.TCP, e.Port)
			continue
		}
		sc.SetWebHandler(&ipn.HTTPHandler{Proxy: target}, host, e.Port, e.Path, k == serveHTTPS, "")
		hp := ipn.HostPort(net.JoinHostPort(host, strconv.Itoa(int(e.Port))))
		if next.Web == nil {
			next.Web = map[ipn.HostPort][]string{}
		}
		next.Web[hp] = append(next.Web[hp], e.Path)
	}
	return next
}

// removeWebOnPort drops every web handler on port, under any host name.
func removeWebOnPort(sc *ipn.ServeConfig, port uint16) {
	for hp := range sc.Web {
		if p, err := hp.Port(); err == nil && p == port {
			delete(sc.Web, hp)
			delete(sc.AllowFunnel, hp)
		}
	}
}

// syncServe applies the configured serve entries once the node has a
// MagicDNS name, and again whenever that name changes.
func (t *Tailnet) syncServe(ctx context.Context, host string) {
	if host == "" {
		return
	}
	t.serveMu.Lock()
	defer t.serveMu.Unlock()
	t.mu.Lock()
	done := t.serveHost == host
	t.mu.Unlock()
	if done {
		return
	}
	err := t.applyServeConfig(ctx, host)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.serveHost, t.serveErr = host, ""
		return
	}
	msg := "serve: " + err.Error()
	if msg != t.serveErr {
		t.logf("%s", msg)
	}
	t.serveErr = msg
}

func (t *Tailnet) applyServeConfig(ctx context.Context, host string) error {
	path := filepath.Join(t.srv.Dir, serveRecordFile)
	prev, err := loadServeRecord(path)
	if err != nil {
		return err
	}
	if len(t.cfg.Serve) == 0 && prev.empty() {
		return nil
	}
	sc, err := t.lc.GetServeConfig(ctx)
	if err != nil {
		return err
	}
	before, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	next := applyServes(sc, prev, t.cfg.Serve, host)
	after, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		if err := t.lc.SetServeConfig(ctx, sc); err != nil {
			return err
		}
		t.logf("serve: applied %d entries from the config", len(t.cfg.Serve))
	}
	return saveServeRecord(path, next)
}
