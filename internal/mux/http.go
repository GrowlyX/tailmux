package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
)

// HTTPHandler serves three things on one port:
//   - an HTTP proxy (CONNECT and absolute-URI requests),
//   - GET /proxy.pac, which sends only tailnet destinations to the proxy,
//   - a small JSON API: /status, /resolve?host=.
func (m *Mux) HTTPHandler(socksAddr string) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /status", m.serveStatus)
	api.HandleFunc("GET /resolve", m.serveResolve)
	api.HandleFunc("GET /stats", m.serveStats)
	api.HandleFunc("POST /tailnets/{name}/{action}", m.serveToggle)
	api.HandleFunc("GET /proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		fmt.Fprint(w, m.PAC(r.Host, socksAddr))
	})

	fwd := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = pr.In.URL
			pr.Out.Host = pr.In.Host
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				c, tgt, err := m.Dial(ctx, network, addr)
				m.LogDial("http", tgt, err)
				return c, err
			},
			ForceAttemptHTTP2:   false,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodConnect:
			m.serveConnect(w, r)
		case r.URL.IsAbs():
			fwd.ServeHTTP(w, r)
		default:
			api.ServeHTTP(w, r)
		}
	})
}

func (m *Mux) serveConnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	up, tgt, err := m.Dial(ctx, "tcp", r.Host)
	cancel()
	m.LogDial("connect", tgt, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer up.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	c, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	pipe(c, brw.Reader, up)
}

type Status struct {
	Tailnets  []TailnetStatus `json:"tailnets"`
	Conflicts []Conflict      `json:"conflicts,omitempty"`
	TUN       any             `json:"tun,omitempty"`
}

// Conflict is a destination more than one tailnet claims.
type Conflict struct {
	What     string   `json:"what"`
	Tailnets []string `json:"tailnets"`
	Winner   string   `json:"winner"`
	Pinned   bool     `json:"pinned,omitempty"`
}

func (m *Mux) Status() Status {
	var s Status
	for _, t := range m.tailnets {
		s.Tailnets = append(s.Tailnets, t.Status())
	}
	s.Conflicts = m.Router().Conflicts()
	if m.TUNStatus != nil {
		s.TUN = m.TUNStatus()
	}
	return s
}

// Conflicts lists subnet routes and short hostnames claimed by several
// tailnets, and who wins each.
func (r *Router) Conflicts() []Conflict {
	routes := map[netip.Prefix][]string{}
	names := map[string][]string{}
	for _, n := range r.nets {
		if !n.Running {
			continue
		}
		seenR, seenN := map[netip.Prefix]bool{}, map[string]bool{}
		for _, p := range n.Peers {
			for _, pfx := range p.Routes {
				if !seenR[pfx] {
					seenR[pfx] = true
					routes[pfx] = append(routes[pfx], n.Name)
				}
			}
			if !seenN[p.Name] {
				seenN[p.Name] = true
				names[p.Name] = append(names[p.Name], n.Name)
			}
		}
	}
	var out []Conflict
	for pfx, tns := range routes {
		if len(tns) > 1 {
			d := r.RouteIP(pfx.Addr())
			out = append(out, Conflict{What: pfx.String(), Tailnets: tns, Winner: d.Tailnet, Pinned: d.Kind == KindPinned})
		}
	}
	for name, tns := range names {
		if len(tns) > 1 {
			d := r.RouteName(name)
			out = append(out, Conflict{What: name, Tailnets: tns, Winner: d.Tailnet, Pinned: d.Kind == KindPinned})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].What < out[j].What })
	return out
}

func (m *Mux) serveStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, m.Status())
}

func (m *Mux) serveResolve(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	if host == "" {
		http.Error(w, "missing ?host=", http.StatusBadRequest)
		return
	}
	tgt, err := m.Resolve(r.Context(), host)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(w, map[string]any{"target": tgt, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"target": tgt})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// PAC builds a proxy auto-config script from the live routing table, so
// browsers (or macOS "Automatic Proxy Configuration") send only tailnet
// traffic through tailmux and everything else direct.
func (m *Mux) PAC(httpHost, socksAddr string) string {
	r := m.Router()
	var doms []string
	var nets []netip.Prefix
	for _, n := range r.nets {
		doms = append(doms, n.Name) // host.<tailnet-name>
		if n.Suffix != "" {
			doms = append(doms, n.Suffix)
		}
		doms = append(doms, n.SplitDNS...)
		for _, p := range n.Peers {
			for _, ip := range p.IPs {
				if ip.Is4() {
					nets = append(nets, netip.PrefixFrom(ip, 32))
				}
			}
			for _, pfx := range p.Routes {
				if pfx.Addr().Is4() {
					nets = append(nets, pfx)
				}
			}
		}
	}
	for d := range r.pins.Domains {
		doms = append(doms, d)
	}
	for p := range r.pins.Prefixes {
		if p.Addr().Is4() {
			nets = append(nets, p)
		}
	}
	// Tailscale's CGNAT range covers every peer, even ones that join
	// after the browser cached this file; individual peer IPs inside it
	// are dropped below as redundant.
	nets = append(nets, netip.MustParsePrefix("100.64.0.0/10"))

	socks := socksAddr
	if h, p, err := net.SplitHostPort(socksAddr); err == nil && (h == "" || h == "0.0.0.0" || h == "::") {
		socks = net.JoinHostPort("127.0.0.1", p)
	}
	proxy := fmt.Sprintf("SOCKS5 %s; SOCKS %s; PROXY %s", socks, socks, httpHost)

	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	b.WriteString("  host = host.toLowerCase();\n")
	fmt.Fprintf(&b, "  var P = %q;\n", proxy)
	sort.Strings(doms)
	for _, d := range uniq(doms) {
		fmt.Fprintf(&b, "  if (host == %q || dnsDomainIs(host, %q)) return P;\n", d, "."+d)
	}
	b.WriteString("  if (/^\\d+\\.\\d+\\.\\d+\\.\\d+$/.test(host)) {\n")
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	nets = slices.DeleteFunc(nets, func(p netip.Prefix) bool { return p != cgnat && cgnat.Contains(p.Addr()) && p.Bits() >= cgnat.Bits() })
	for i := range nets {
		nets[i] = nets[i].Masked()
	}
	slices.SortFunc(nets, func(a, b netip.Prefix) int { return a.Compare(b) })
	nets = slices.Compact(nets)
	for _, p := range nets {
		mask := net.CIDRMask(p.Bits(), 32)
		fmt.Fprintf(&b, "    if (isInNet(host, %q, %q)) return P;\n", p.Addr().String(), net.IP(mask).String())
	}
	b.WriteString("  }\n")
	// Bare names that match a peer somewhere.
	var shorts []string
	for _, n := range r.nets {
		for _, p := range n.Peers {
			shorts = append(shorts, p.Name)
		}
	}
	sort.Strings(shorts)
	if s := uniq(shorts); len(s) > 0 {
		js, _ := json.Marshal(s)
		fmt.Fprintf(&b, "  if (%s.indexOf(host) >= 0) return P;\n", js)
	}
	b.WriteString("  return \"DIRECT\";\n}\n")
	return b.String()
}

func uniq(s []string) []string {
	s = slices.Compact(s)
	return slices.DeleteFunc(s, func(v string) bool { return v == "" })
}
