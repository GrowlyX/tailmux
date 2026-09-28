// Package setup is `tailmux setup`: a terminal UI for writing the config
// and logging each tailnet in, so nobody has to hand-edit JSON.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/GrowlyX/tailmux/internal/mux"
)

// Run opens the setup UI on the config at path (created if missing).
func Run(path string) error {
	cfg, err := mux.ReadConfig(path)
	if err != nil {
		return err
	}
	// tsnet logs through the standard logger; keep it off the screen.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	m := newModel(path, cfg)
	final, err := tea.NewProgram(m).Run()
	if fm, ok := final.(*model); ok && fm.login != nil && fm.login.mux != nil {
		fm.login.mux.Close()
	}
	return err
}

type screen int

const (
	screenList screen = iota
	screenForm
	screenLogin
)

type model struct {
	path   string
	cfg    *mux.Config
	cursor int
	screen screen
	dirty  bool
	note   string
	quitOK bool // q pressed once with unsaved changes
	width  int

	form     *huh.Form
	formDone func()

	daemon map[string]mux.TailnetStatus // from a running daemon, if any
	login  *loginState
}

func newModel(path string, cfg *mux.Config) *model {
	m := &model{path: path, cfg: cfg, width: 80}
	if len(cfg.Tailnets) == 0 {
		m.note = "No tailnets yet. Press a to add one."
	}
	return m
}

// effective is the config with defaults filled in, for addresses and
// state paths. Errors (say, zero tailnets) just mean fewer defaults.
func (m *model) effective() *mux.Config {
	b, _ := json.Marshal(m.cfg)
	var c mux.Config
	json.Unmarshal(b, &c)
	c.Normalize()
	return &c
}

// --- messages ---

type daemonMsg map[string]mux.TailnetStatus
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) pollDaemon() tea.Cmd {
	addr := m.effective().HTTP
	return func() tea.Msg {
		c := &http.Client{Timeout: 800 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
		resp, err := c.Get("http://" + addr + "/status")
		if err != nil {
			return daemonMsg(nil)
		}
		defer resp.Body.Close()
		var st mux.Status
		if json.NewDecoder(resp.Body).Decode(&st) != nil {
			return daemonMsg(nil)
		}
		out := daemonMsg{}
		for _, t := range st.Tailnets {
			out[t.Name] = t
		}
		return out
	}
}

func (m *model) Init() tea.Cmd { return tea.Batch(m.pollDaemon(), tick()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case daemonMsg:
		m.daemon = msg
		return m, nil
	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if m.screen != screenLogin || m.login.viaDaemon {
			cmds = append(cmds, m.pollDaemon())
		}
		if m.screen == screenLogin {
			m.login.refresh(m)
		}
		return m, tea.Batch(cmds...)
	case loginStartedMsg:
		if m.login != nil {
			m.login.started(msg)
		}
		return m, nil
	}

	switch m.screen {
	case screenForm:
		return m.updateForm(msg)
	case screenLogin:
		return m.updateLogin(msg)
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		return m.updateList(k)
	}
	return m, nil
}

func (m *model) updateList(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.cfg.Tailnets)
	key := k.String()
	if key != "q" {
		m.quitOK = false
	}
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < n-1 {
			m.cursor++
		}
	case "K", "shift+up":
		if m.cursor > 0 {
			t := m.cfg.Tailnets
			t[m.cursor], t[m.cursor-1] = t[m.cursor-1], t[m.cursor]
			m.cursor--
			m.changed("Moved up. Earlier tailnets win ties.")
		}
	case "J", "shift+down":
		if m.cursor < n-1 {
			t := m.cfg.Tailnets
			t[m.cursor], t[m.cursor+1] = t[m.cursor+1], t[m.cursor]
			m.cursor++
			m.changed("Moved down.")
		}
	case "a":
		return m, m.tailnetForm(-1)
	case "e", "enter":
		if n > 0 {
			return m, m.tailnetForm(m.cursor)
		}
	case "d", "backspace", "delete":
		if n > 0 {
			name := m.cfg.Tailnets[m.cursor].Name
			m.cfg.Tailnets = slices.Delete(m.cfg.Tailnets, m.cursor, m.cursor+1)
			for k, v := range m.cfg.Pins {
				if v == name {
					delete(m.cfg.Pins, k)
				}
			}
			m.cursor = max(0, min(m.cursor, len(m.cfg.Tailnets)-1))
			m.changed("Removed " + name + ". Its login stays in the state dir.")
		}
	case "t":
		m.cfg.TUN.Enabled = !m.cfg.TUN.Enabled
		if m.cfg.TUN.Enabled {
			m.changed("TUN mode on: every app reaches tailnets. Needs root (sudo).")
		} else {
			m.changed("TUN mode off: proxy mode only.")
		}
	case "p":
		return m, m.pinsForm()
	case "o":
		return m, m.settingsForm()
	case "s":
		m.save()
	case "l":
		if n == 0 {
			m.note = "Add a tailnet first."
			return m, nil
		}
		if m.dirty {
			m.save()
		}
		return m, m.startLogin()
	case "q", "esc", "ctrl+c":
		if m.dirty && !m.quitOK && key != "ctrl+c" {
			m.quitOK = true
			m.note = "Unsaved changes. s to save, q again to discard."
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) changed(note string) {
	m.dirty = true
	m.note = note
}

func (m *model) save() {
	if err := m.cfg.Save(m.path); err != nil {
		m.note = "Save failed: " + err.Error()
		return
	}
	m.dirty = false
	m.note = "Saved " + m.path + ". Press l to log in."
	if m.daemon != nil {
		m.note = "Saved. Restart the daemon to pick it up (brew services restart tailmux)."
	}
}

// --- forms ---

func (m *model) openForm(f *huh.Form, done func()) tea.Cmd {
	m.form = f.WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true).WithWidth(min(m.width-4, 72))
	m.formDone = done
	m.screen = screenForm
	return m.form.Init()
}

func (m *model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		m.screen = screenList
		m.note = "Cancelled."
		return m, nil
	}
	f, cmd := m.form.Update(msg)
	if ff, ok := f.(*huh.Form); ok {
		m.form = ff
	}
	switch m.form.State {
	case huh.StateCompleted:
		m.formDone()
		m.screen = screenList
		return m, nil
	case huh.StateAborted:
		m.screen = screenList
		m.note = "Cancelled."
		return m, nil
	}
	return m, cmd
}

func (m *model) tailnetForm(idx int) tea.Cmd {
	var tn mux.TailnetConfig
	if idx >= 0 {
		tn = m.cfg.Tailnets[idx]
	}
	name, control, key := tn.Name, tn.ControlURL, tn.AuthKey
	orig := tn.Name
	title := "Add a tailnet"
	if idx >= 0 {
		title = "Edit " + orig
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewNote().Title(title).Description("One entry per tailnet. tailmux joins each as its own device."),
		huh.NewInput().Title("Name").
			Description("A short label. Also works as a DNS suffix: web.<name>").
			Placeholder("work").Value(&name).
			Validate(func(s string) error {
				s = strings.ToLower(strings.TrimSpace(s))
				if s == "" || strings.ContainsAny(s, ". /\t") {
					return fmt.Errorf("one word, no dots or spaces")
				}
				for _, t := range m.cfg.Tailnets {
					if t.Name == s && s != orig {
						return fmt.Errorf("%q already exists", s)
					}
				}
				return nil
			}),
		huh.NewInput().Title("Control server").
			Description("Leave empty for Tailscale. Set a URL for Headscale.").
			Placeholder("https://controlplane.tailscale.com").Value(&control).
			Validate(func(s string) error {
				if s != "" && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
					return fmt.Errorf("must start with https://")
				}
				return nil
			}),
		huh.NewInput().Title("Auth key (optional)").
			Description("Empty: log in through the browser. env:VAR reads it from the environment.").
			EchoMode(huh.EchoModePassword).Value(&key),
	))
	return m.openForm(f, func() {
		tn.Name = strings.ToLower(strings.TrimSpace(name))
		tn.ControlURL = strings.TrimSpace(control)
		tn.AuthKey = strings.TrimSpace(key)
		if idx < 0 {
			m.cfg.Tailnets = append(m.cfg.Tailnets, tn)
			m.cursor = len(m.cfg.Tailnets) - 1
			m.changed("Added " + tn.Name + ". Press s to save, l to log in.")
			return
		}
		m.cfg.Tailnets[idx] = tn
		if orig != tn.Name {
			for k, v := range m.cfg.Pins {
				if v == orig {
					m.cfg.Pins[k] = tn.Name
				}
			}
		}
		m.changed("Updated " + tn.Name + ".")
	})
}

func (m *model) pinsForm() tea.Cmd {
	var lines []string
	for k, v := range m.cfg.Pins {
		lines = append(lines, k+" = "+v)
	}
	slices.Sort(lines)
	text := strings.Join(lines, "\n")
	names := make([]string, 0, len(m.cfg.Tailnets))
	for _, t := range m.cfg.Tailnets {
		names = append(names, t.Name)
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewText().Title("Pins").
			Description("Force a subnet or domain to one tailnet, one per line:\n  10.0.0.0/24 = home\n  corp.internal = work\nTailnets: " + strings.Join(names, ", ")).
			Value(&text).Lines(8).
			Validate(func(s string) error { _, err := parsePins(s, names); return err }),
	))
	return m.openForm(f, func() {
		pins, _ := parsePins(text, names)
		m.cfg.Pins = pins
		m.changed(fmt.Sprintf("%d pin(s).", len(pins)))
	})
}

func parsePins(s string, names []string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.ToLower(strings.TrimSpace(v))
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("line %d: want <subnet or domain> = <tailnet>", i+1)
		}
		if !slices.Contains(names, v) {
			return nil, fmt.Errorf("line %d: no tailnet named %q", i+1, v)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (m *model) settingsForm() tea.Cmd {
	eff := m.effective()
	host, socks, httpAddr := m.cfg.Hostname, m.cfg.SOCKS5, m.cfg.HTTP
	cgnat := m.cfg.TUN.CGNAT
	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Device name").Description("How this machine shows up in every tailnet.").
			Placeholder(eff.Hostname).Value(&host),
		huh.NewInput().Title("SOCKS5 address").Placeholder(eff.SOCKS5).Value(&socks),
		huh.NewInput().Title("HTTP proxy / API address").Description("The menu bar app and `tailmux status` use this.").
			Placeholder(eff.HTTP).Value(&httpAddr),
		huh.NewConfirm().Title("In TUN mode, also route 100.64.0.0/10?").
			Description("Off leaves Tailscale's own range to the official app.").Value(&cgnat),
	))
	return m.openForm(f, func() {
		m.cfg.Hostname, m.cfg.SOCKS5, m.cfg.HTTP = strings.TrimSpace(host), strings.TrimSpace(socks), strings.TrimSpace(httpAddr)
		m.cfg.TUN.CGNAT = cgnat
		m.changed("Settings updated.")
	})
}

// --- views ---

var (
	accent  = lipgloss.Color("#7D56F4")
	green   = lipgloss.Color("#3FB950")
	orange  = lipgloss.Color("#E3A03A")
	dim     = lipgloss.Color("#8B8B8B")
	title   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	faint   = lipgloss.NewStyle().Foreground(dim)
	sel     = lipgloss.NewStyle().Foreground(accent).Bold(true)
	okStyle = lipgloss.NewStyle().Foreground(green)
	warn    = lipgloss.NewStyle().Foreground(orange)
	box     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#444444")).Padding(0, 1)
)

func (m *model) View() tea.View {
	var s string
	switch m.screen {
	case screenForm:
		s = m.header() + "\n\n" + m.form.View()
	case screenLogin:
		s = m.loginView()
	default:
		s = m.listView()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

func (m *model) header() string {
	d := ""
	if m.dirty {
		d = warn.Render(" • unsaved")
	}
	return title.Render("tailmux setup") + "  " + faint.Render(m.path) + d
}

func (m *model) listView() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n\n")
	if len(m.cfg.Tailnets) == 0 {
		b.WriteString(faint.Render("  (no tailnets)") + "\n")
	}
	eff := m.effective()
	for i, t := range m.cfg.Tailnets {
		cur := "  "
		name := fmt.Sprintf("%-18s", t.Name)
		if i == m.cursor {
			cur = sel.Render("▸ ")
			name = sel.Render(name)
		}
		control := "tailscale"
		if t.ControlURL != "" {
			control = strings.TrimPrefix(strings.TrimPrefix(t.ControlURL, "https://"), "http://")
		}
		auth := "browser"
		if t.AuthKey != "" {
			auth = "auth key"
		}
		fmt.Fprintf(&b, "%s%d  %s %s %s %s\n", cur, i+1, name,
			faint.Render(fmt.Sprintf("%-22s", trunc(control, 22))),
			faint.Render(fmt.Sprintf("%-9s", auth)),
			m.statusCell(t.Name, eff.StateDir))
	}
	b.WriteString("\n")
	tun := faint.Render("off")
	if m.cfg.TUN.Enabled {
		tun = okStyle.Render("on") + faint.Render(" (runs as root)")
	}
	fmt.Fprintf(&b, "  TUN mode  %s     proxies %s  %s\n", tun, faint.Render(eff.SOCKS5), faint.Render(eff.HTTP))
	if len(m.cfg.Pins) > 0 {
		fmt.Fprintf(&b, "  pins      %s\n", faint.Render(fmt.Sprintf("%d", len(m.cfg.Pins))))
	}
	if m.daemon != nil {
		b.WriteString("  daemon    " + okStyle.Render("running") + "\n")
	} else {
		b.WriteString("  daemon    " + faint.Render("not running") + "\n")
	}
	b.WriteString("\n")
	if m.note != "" {
		b.WriteString("  " + m.note + "\n\n")
	}
	b.WriteString(faint.Render("  a add · e edit · d delete · J/K reorder · t TUN · p pins · o options · l log in · s save · q quit"))
	return b.String()
}

func (m *model) statusCell(name, stateDir string) string {
	if st, ok := m.daemon[name]; ok {
		return stateLabel(st)
	}
	if _, err := os.Stat(filepath.Join(stateDir, name, "tailscaled.state")); err == nil {
		return faint.Render("○ logged in before")
	}
	return faint.Render("· not joined")
}

func stateLabel(st mux.TailnetStatus) string {
	switch {
	case !st.Enabled:
		return faint.Render("○ off")
	case st.AuthURL != "" || st.State == "NeedsLogin":
		return warn.Render("! needs login")
	case st.State == "Running":
		return okStyle.Render(fmt.Sprintf("● connected (%d/%d online)", st.Online, st.Peers))
	case st.State == "":
		return faint.Render("… starting")
	}
	return faint.Render("… " + strings.ToLower(st.State))
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// --- login ---

type loginState struct {
	viaDaemon bool
	mux       *mux.Mux
	cancel    context.CancelFunc
	err       string
	cursor    int
	statuses  []mux.TailnetStatus
	opened    map[string]bool
}

type loginStartedMsg struct {
	m   *mux.Mux
	err error
}

func (m *model) startLogin() tea.Cmd {
	m.screen = screenLogin
	m.login = &loginState{opened: map[string]bool{}}
	if m.daemon != nil {
		// The daemon owns the state dir; log in through it.
		m.login.viaDaemon = true
		m.login.refresh(m)
		return nil
	}
	cfg := m.effective()
	ctx, cancel := context.WithCancel(context.Background())
	m.login.cancel = cancel
	return func() tea.Msg {
		x := mux.New(cfg, mux.Options{})
		if err := x.Start(ctx); err != nil {
			x.Close()
			return loginStartedMsg{err: err}
		}
		return loginStartedMsg{m: x}
	}
}

func (l *loginState) started(msg loginStartedMsg) {
	if msg.err != nil {
		l.err = msg.err.Error()
		return
	}
	l.mux = msg.m
}

func (l *loginState) refresh(m *model) {
	switch {
	case l.viaDaemon:
		l.statuses = l.statuses[:0]
		for _, t := range m.cfg.Tailnets {
			st, ok := m.daemon[t.Name]
			if !ok {
				st = mux.TailnetStatus{Name: t.Name, State: "not in the running daemon; restart it", Enabled: true}
			}
			l.statuses = append(l.statuses, st)
		}
	case l.mux != nil:
		l.statuses = l.mux.Status().Tailnets
	}
}

func (l *loginState) stop() {
	if l.cancel != nil {
		l.cancel()
	}
	if l.mux != nil {
		l.mux.Close()
		l.mux = nil
	}
}

func (m *model) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	l := m.login
	switch k.String() {
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
	case "down", "j":
		if l.cursor < len(l.statuses)-1 {
			l.cursor++
		}
	case "enter", "o":
		if l.cursor < len(l.statuses) {
			if u := l.statuses[l.cursor].AuthURL; u != "" {
				openBrowser(u)
				l.opened[l.statuses[l.cursor].Name] = true
			}
		}
	case "esc", "q", "ctrl+c":
		l.stop()
		m.screen = screenList
		m.note = m.loginSummary()
		if k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, m.pollDaemon()
	}
	return m, nil
}

func (m *model) loginSummary() string {
	ok := 0
	for _, s := range m.login.statuses {
		if s.State == "Running" {
			ok++
		}
	}
	return fmt.Sprintf("%d of %d tailnets logged in.", ok, len(m.cfg.Tailnets))
}

func (m *model) loginView() string {
	l := m.login
	var b strings.Builder
	b.WriteString(title.Render("tailmux setup · log in") + "\n\n")
	if l.viaDaemon {
		b.WriteString(faint.Render("  Logging in through the running daemon.") + "\n\n")
	}
	if l.err != "" {
		b.WriteString(warn.Render("  "+l.err) + "\n\n")
	}
	if len(l.statuses) == 0 && l.err == "" {
		b.WriteString(faint.Render("  Starting tailnets…") + "\n")
	}
	done := 0
	for i, st := range l.statuses {
		cur := "  "
		name := fmt.Sprintf("%-18s", st.Name)
		if i == l.cursor {
			cur = sel.Render("▸ ")
			name = sel.Render(name)
		}
		if st.State == "Running" {
			done++
		}
		line := fmt.Sprintf("%s%s %s", cur, name, stateLabel(st))
		if st.State == "Running" && len(st.SelfIPs) > 0 {
			line += faint.Render("  " + st.Suffix + "  " + st.SelfIPs[0])
		}
		b.WriteString(line + "\n")
		if st.AuthURL != "" {
			hint := "enter to open"
			if l.opened[st.Name] {
				hint = "opened; finish signing in"
			}
			b.WriteString("      " + faint.Render(st.AuthURL+"  ("+hint+")") + "\n")
		}
	}
	b.WriteString("\n")
	if n := len(l.statuses); n > 0 && done == n {
		b.WriteString(okStyle.Render(fmt.Sprintf("  All %d tailnets connected.", n)) + " " + faint.Render("Press esc to go back.") + "\n")
		b.WriteString(faint.Render("  Next: "+startHint(m.cfg.TUN.Enabled)) + "\n")
	} else {
		b.WriteString(faint.Render("  Each login page belongs to one tailnet: sign in with the account that owns it.") + "\n")
	}
	b.WriteString("\n" + faint.Render("  ↑/↓ select · enter open login page · esc back"))
	return box.Render(b.String())
}

func startHint(tun bool) string {
	if tun {
		return "sudo brew services start tailmux  (or: sudo tailmux up)"
	}
	return "brew services start tailmux  (or: tailmux up)"
}

func openBrowser(url string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	exec.Command(cmd, url).Start()
}
