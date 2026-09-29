package update

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Status is what /status reports about updates.
type Status struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	URL       string    `json:"url,omitempty"`
	State     string    `json:"state"` // idle, checking, installing, installed, failed
	Error     string    `json:"error,omitempty"`
	Checked   time.Time `json:"checked,omitzero"`
	Auto      bool      `json:"auto"`
	Method    Method    `json:"method"`
}

// Manager checks for releases on a schedule and, if Auto, installs them.
// The restart that follows an install comes from WatchExecutable.
type Manager struct {
	Current  string
	Auto     bool
	Interval time.Duration
	Delay    time.Duration // before the first check; TAILMUX_UPDATE_DELAY
	Exe      string        // resolved path of the running binary

	mu     sync.Mutex
	st     Status
	busy   bool
	latest *Release
}

func NewManager(current string, auto bool) *Manager {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	m := &Manager{Current: current, Auto: auto, Interval: 6 * time.Hour, Delay: 30 * time.Second, Exe: exe}
	if d, err := time.ParseDuration(os.Getenv("TAILMUX_UPDATE_DELAY")); err == nil {
		m.Delay = d
	}
	method, _ := Detect(exe)
	m.st = Status{Current: current, State: "idle", Auto: auto, Method: method}
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// Run checks shortly after start and then every Interval.
func (m *Manager) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(m.Delay): // let tailnets come up first
	}
	for {
		m.checkAndMaybeInstall(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.Interval):
		}
	}
}

func (m *Manager) checkAndMaybeInstall(ctx context.Context) {
	if _, err := m.Check(ctx); err != nil {
		log.Printf("update check: %v", err)
		return
	}
	if st := m.Status(); st.Available && m.Auto {
		if err := m.Install(ctx); err != nil {
			log.Printf("update: %v", err)
		}
	}
}

// Check asks GitHub for the latest release.
func (m *Manager) Check(ctx context.Context) (Status, error) {
	m.set(func(s *Status) { s.State = "checking" })
	rel, err := Latest(ctx)
	if err != nil {
		m.set(func(s *Status) { s.State = "failed"; s.Error = err.Error() })
		return m.Status(), err
	}
	m.mu.Lock()
	m.latest = rel
	m.st.Latest = rel.Version()
	m.st.URL = rel.URL
	m.st.Available = Newer(m.Current, rel.Version())
	m.st.Checked = time.Now()
	m.st.State, m.st.Error = "idle", ""
	st := m.st
	m.mu.Unlock()
	if st.Available {
		log.Printf("update available: %s -> %s (%s)", m.Current, st.Latest, st.URL)
	}
	return st, nil
}

var ErrBusy = errors.New("an update is already running")

// Install installs the latest release (checking first if needed).
func (m *Manager) Install(ctx context.Context) error {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return ErrBusy
	}
	m.busy = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock() }()

	// Always ask again: the last check can be hours old, and the Update
	// button is pressed precisely because something new came out.
	if _, err := m.Check(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	rel := m.latest
	m.mu.Unlock()
	if !Newer(m.Current, rel.Version()) {
		return nil
	}
	m.set(func(s *Status) { s.State = "installing"; s.Error = "" })
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	err := Install(ctx, rel, m.Exe, func(f string, a ...any) { log.Printf("update: "+f, a...) })
	if err != nil {
		m.set(func(s *Status) { s.State = "failed"; s.Error = err.Error() })
		return err
	}
	m.set(func(s *Status) { s.State = "installed" })
	log.Printf("update: %s installed; restarting onto it", rel.Tag)
	return nil
}

func (m *Manager) set(f func(*Status)) {
	m.mu.Lock()
	f(&m.st)
	m.mu.Unlock()
}

// WatchExecutable calls onChange once the program at invokedPath (as it
// was started, e.g. /opt/homebrew/opt/tailmux/bin/tailmux) now resolves
// to a different or modified file: an upgrade happened.
func WatchExecutable(ctx context.Context, invokedPath string, every time.Duration, onChange func()) {
	fp := func() string {
		r, err := filepath.EvalSymlinks(invokedPath)
		if err != nil {
			return ""
		}
		st, err := os.Stat(r)
		if err != nil {
			return ""
		}
		return fmt.Sprint(r, "|", st.ModTime().UnixNano(), "|", st.Size())
	}
	start := fp()
	if start == "" {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if now := fp(); now != "" && now != start {
				onChange()
				return
			}
		}
	}
}

// InvokedPath is the path this process was started as, before symlinks
// are resolved, so a re-exec picks up whatever it points at now.
func InvokedPath() string {
	p := os.Args[0]
	if !filepath.IsAbs(p) {
		if lp, err := exec.LookPath(p); err == nil {
			p = lp
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
