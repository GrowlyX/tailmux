package mux

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Managing tailmux from the desktop apps: add and remove tailnets while
// running, change settings, read recent logs. Every change is saved to
// ConfigPath so the next start agrees with what the app shows.

// validName reports a problem with a tailnet name, or "".
func validName(name string) string {
	if name == "" || strings.ContainsAny(name, ". /\t\\") {
		return "the name must be one word, without dots or spaces"
	}
	return ""
}

// AddTailnet joins a new tailnet now and saves it to the config.
func (m *Mux) AddTailnet(tc TailnetConfig) (*Tailnet, error) {
	tc.Name = strings.ToLower(strings.TrimSpace(tc.Name))
	if msg := validName(tc.Name); msg != "" {
		return nil, errors.New(msg)
	}
	if v, ok := strings.CutPrefix(tc.AuthKey, "env:"); ok {
		tc.AuthKey = os.Getenv(v)
	}
	if tc.Hostname == "" {
		tc.Hostname = m.cfg.Hostname
	}
	m.tmu.Lock()
	if m.byName[tc.Name] != nil {
		m.tmu.Unlock()
		return nil, fmt.Errorf("a tailnet named %q already exists", tc.Name)
	}
	prio := 0
	for _, t := range m.tailnets {
		prio = max(prio, t.priority+1)
	}
	t := newTailnet(tc, prio, tailnetOpts{stateDir: m.cfg.StateDir, verbose: m.opts.Verbose, memStore: m.opts.MemStore}, m.rebuild)
	m.tailnets = append(m.tailnets, t)
	m.byName[tc.Name] = t
	ctx := m.ctx
	m.tmu.Unlock()

	if ctx != nil {
		if err := t.start(ctx); err != nil {
			m.dropTailnet(tc.Name)
			t.close()
			return nil, err
		}
	}
	m.rebuild()
	if err := m.editConfig(func(c *Config) {
		raw := tc
		raw.Hostname = "" // keep the config's own default
		c.Tailnets = append(c.Tailnets, raw)
	}); err != nil {
		return t, fmt.Errorf("joined, but saving the config failed: %w", err)
	}
	return t, nil
}

// RemoveTailnet leaves a tailnet and removes it from the config. Its
// login stays in the state directory, so adding it back needs no login.
func (m *Mux) RemoveTailnet(name string) error {
	t := m.dropTailnet(name)
	if t == nil {
		return os.ErrNotExist
	}
	go t.close()
	if e := m.ExitNode(); e != nil && e.Tailnet == name {
		m.exitCfg.Store(nil)
	}
	m.rebuild()
	m.saveDisabled()
	return m.editConfig(func(c *Config) {
		if c.ExitNode != nil && strings.EqualFold(c.ExitNode.Tailnet, name) {
			c.ExitNode = nil
		}
		c.Tailnets = slices.DeleteFunc(c.Tailnets, func(x TailnetConfig) bool { return strings.EqualFold(x.Name, name) })
		for k, v := range c.Pins {
			if strings.EqualFold(v, name) {
				delete(c.Pins, k)
			}
		}
	})
}

func (m *Mux) dropTailnet(name string) *Tailnet {
	m.tmu.Lock()
	defer m.tmu.Unlock()
	t := m.byName[name]
	if t == nil {
		return nil
	}
	delete(m.byName, name)
	m.tailnets = slices.DeleteFunc(m.tailnets, func(x *Tailnet) bool { return x == t })
	return t
}

// Settings are the options the apps expose. Nil fields are left alone.
// They apply on the next start; the apps offer to restart.
type Settings struct {
	TUN        *bool   `json:"tun,omitempty"`
	AutoUpdate *bool   `json:"auto_update,omitempty"`
	Hostname   *string `json:"hostname,omitempty"`
}

func (m *Mux) SetSettings(s Settings) error {
	return m.editConfig(func(c *Config) {
		if s.TUN != nil {
			c.TUN.Enabled = *s.TUN
		}
		if s.AutoUpdate != nil {
			if *s.AutoUpdate {
				c.Updates.Auto = nil // default
			} else {
				c.Updates.Auto = s.AutoUpdate
			}
		}
		if s.Hostname != nil {
			c.Hostname = strings.TrimSpace(*s.Hostname)
		}
	})
}

var configMu sync.Mutex

// editConfig applies f to the config file as written (not the running,
// defaults-filled copy) and saves it with its original owner.
func (m *Mux) editConfig(f func(*Config)) error {
	if m.ConfigPath == "" {
		return nil
	}
	configMu.Lock()
	defer configMu.Unlock()
	c, err := ReadConfig(m.ConfigPath)
	if err != nil {
		return err
	}
	f(c)
	return c.Save(m.ConfigPath)
}

// ConfigView is GET /config: the saved config with auth keys hidden,
// plus where things live.
type ConfigView struct {
	Path     string  `json:"path"`
	StateDir string  `json:"state_dir"`
	Config   *Config `json:"config"`
}

func (m *Mux) configView() (ConfigView, error) {
	v := ConfigView{Path: m.ConfigPath, StateDir: m.cfg.StateDir}
	c := &Config{}
	if m.ConfigPath != "" {
		var err error
		if c, err = ReadConfig(m.ConfigPath); err != nil {
			return v, err
		}
	}
	for i := range c.Tailnets {
		if c.Tailnets[i].AuthKey != "" && !strings.HasPrefix(c.Tailnets[i].AuthKey, "env:") {
			c.Tailnets[i].AuthKey = "(hidden)"
		}
	}
	v.Config = c
	return v, nil
}

// --- HTTP ---

// guard enforces the X-Tailmux header on every endpoint that changes
// something, so a web page can't reach them with a cross-site request.
func guard(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Tailmux") == "" {
		http.Error(w, "missing X-Tailmux header", http.StatusForbidden)
		return false
	}
	return true
}

func (m *Mux) serveConfig(w http.ResponseWriter, r *http.Request) {
	v, err := m.configView()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (m *Mux) serveAddTailnet(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var tc TailnetConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&tc); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	t, err := m.AddTailnet(tc)
	if err != nil && t == nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Give the node a moment to reach control, so the reply carries the
	// login URL when there is one.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := t.Status(); st.AuthURL != "" || st.State == "Running" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t.Status())
}

func (m *Mux) serveRemoveTailnet(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	if err := m.RemoveTailnet(r.PathValue("name")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Mux) serveSettings(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var s Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&s); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.SetSettings(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"restart_required": true})
}

func (m *Mux) serveRestart(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	if m.Restart == nil {
		http.Error(w, "restart not supported", http.StatusNotImplemented)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(200 * time.Millisecond) // let the reply go out
		m.Restart()
	}()
}

func (m *Mux) serveLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > logRingSize {
		n = 200
	}
	writeJSON(w, Logs.Last(n))
}

// --- log ring ---

const logRingSize = 1000

// Logs keeps the daemon's recent log lines for GET /logs. main tees the
// standard logger into it.
var Logs = &LogRing{}

type LogRing struct {
	mu    sync.Mutex
	lines []string
	part  []byte
}

func (l *LogRing) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.part = append(l.part, p...)
	for {
		i := slices.Index(l.part, '\n')
		if i < 0 {
			break
		}
		l.lines = append(l.lines, string(l.part[:i]))
		l.part = l.part[i+1:]
	}
	if over := len(l.lines) - logRingSize; over > 0 {
		l.lines = slices.Delete(l.lines, 0, over)
	}
	return len(p), nil
}

func (l *LogRing) Last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines[max(0, len(l.lines)-n):])
}
