package mux

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// HistoryLen is how many one-second throughput samples each tailnet keeps.
const HistoryLen = 120

// traffic counts bytes through one tailnet and keeps a short history of
// per-second rates for the menu bar graph.
type traffic struct {
	rx, tx atomic.Uint64
	conns  atomic.Int64

	mu             sync.Mutex
	lastRx, lastTx uint64
	rxHist, txHist []float64 // bytes/s, oldest first
}

func (tr *traffic) sample() {
	rx, tx := tr.rx.Load(), tr.tx.Load()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.rxHist = appendRing(tr.rxHist, float64(rx-tr.lastRx))
	tr.txHist = appendRing(tr.txHist, float64(tx-tr.lastTx))
	tr.lastRx, tr.lastTx = rx, tx
}

func appendRing(h []float64, v float64) []float64 {
	if len(h) >= HistoryLen {
		h = h[1:]
	}
	return append(h, v)
}

func (tr *traffic) history() (rx, tx []float64) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.rxHist), slices.Clone(tr.txHist)
}

// countingConn attributes a connection's bytes to its tailnet. rx is
// what came back from the tailnet, tx what we sent into it.
type countingConn struct {
	net.Conn
	tr   *traffic
	once sync.Once
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.tr.rx.Add(uint64(n))
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.tr.tx.Add(uint64(n))
	return n, err
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.tr.conns.Add(-1) })
	return c.Conn.Close()
}

func (c *countingConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

func (m *Mux) sampleTraffic(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, t := range m.tailnets {
				t.traffic.sample()
			}
		}
	}
}

type TailnetStats struct {
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`
	State   string    `json:"state"`
	Conns   int64     `json:"conns"`
	RxTotal uint64    `json:"rx_total"`
	TxTotal uint64    `json:"tx_total"`
	Rx      []float64 `json:"rx"` // bytes/s, one sample per second, oldest first
	Tx      []float64 `json:"tx"`
}

type Stats struct {
	IntervalMS int            `json:"interval_ms"`
	Tailnets   []TailnetStats `json:"tailnets"`
}

func (m *Mux) Stats() Stats {
	s := Stats{IntervalMS: 1000}
	for _, t := range m.tailnets {
		rx, tx := t.traffic.history()
		st := t.Status()
		s.Tailnets = append(s.Tailnets, TailnetStats{
			Name: t.cfg.Name, Enabled: t.Enabled(), State: st.State,
			Conns:   t.traffic.conns.Load(),
			RxTotal: t.traffic.rx.Load(), TxTotal: t.traffic.tx.Load(),
			Rx: rx, Tx: tx,
		})
	}
	return s
}

// Disabled tailnets are remembered across restarts.
func (m *Mux) disabledPath() string { return filepath.Join(m.cfg.StateDir, "disabled.json") }

func (m *Mux) loadDisabled() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(m.disabledPath())
	if err != nil {
		return out
	}
	var names []string
	json.Unmarshal(b, &names)
	for _, n := range names {
		out[n] = true
	}
	return out
}

func (m *Mux) saveDisabled() error {
	var names []string
	for _, t := range m.tailnets {
		if !t.Enabled() {
			names = append(names, t.cfg.Name)
		}
	}
	b, _ := json.Marshal(names)
	os.MkdirAll(m.cfg.StateDir, 0o700)
	return os.WriteFile(m.disabledPath(), b, 0o600)
}

// SetEnabled takes a tailnet down (like `tailscale down`) or back up.
// A disabled tailnet keeps its login but routes nothing.
func (m *Mux) SetEnabled(ctx context.Context, name string, on bool) error {
	t := m.byName[name]
	if t == nil {
		return os.ErrNotExist
	}
	if err := t.setEnabled(ctx, on); err != nil {
		return err
	}
	return m.saveDisabled()
}

func (m *Mux) serveStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, m.Stats())
}

// serveToggle handles POST /tailnets/{name}/enable and .../disable. The
// X-Tailmux header is required so a web page can't flip tailnets with a
// cross-site form post (custom headers force a CORS preflight, which
// this server never approves).
func (m *Mux) serveToggle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Tailmux") == "" {
		http.Error(w, "missing X-Tailmux header", http.StatusForbidden)
		return
	}
	on := r.PathValue("action") == "enable"
	if !on && r.PathValue("action") != "disable" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := m.SetEnabled(ctx, r.PathValue("name"), on); err != nil {
		code := http.StatusInternalServerError
		if os.IsNotExist(err) {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, m.byName[r.PathValue("name")].Status())
}

// PeerInfo is one device in one tailnet, for `tailmux peers`.
type PeerInfo struct {
	Tailnet string   `json:"tailnet"`
	Name    string   `json:"name"`
	Alias   string   `json:"alias"` // name.tailnet: what to type
	FQDN    string   `json:"fqdn"`
	IPs     []string `json:"ips"`
	Online  bool     `json:"online"`
	Routes  []string `json:"routes,omitempty"`
}

func (m *Mux) Peers() []PeerInfo {
	var out []PeerInfo
	for _, s := range m.Router().Snapshots() {
		for _, p := range s.Peers {
			pi := PeerInfo{Tailnet: s.Name, Name: p.Name, Alias: p.Name + "." + s.Name, FQDN: p.FQDN, Online: p.Online}
			for _, ip := range p.IPs {
				pi.IPs = append(pi.IPs, ip.String())
			}
			for _, r := range p.Routes {
				pi.Routes = append(pi.Routes, r.String())
			}
			out = append(out, pi)
		}
	}
	return out
}

func (m *Mux) servePeers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, m.Peers())
}
