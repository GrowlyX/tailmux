package mux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Logins live under the tailnet's state, not its name: renaming "work"
// to "work2" and then "home" to "work" must leave each login with the
// tailnet it belongs to.
func TestRenameKeepsLogin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"tailnets":[{"name":"work"},{"name":"home"}]}`), 0o600)

	m := New(&Config{StateDir: dir, Tailnets: []TailnetConfig{{Name: "work"}, {Name: "home"}}}, Options{})
	m.ConfigPath = path
	if err := m.PinStates(); err != nil {
		t.Fatal(err)
	}

	// Rename by hand, as someone editing the file would.
	b, _ := os.ReadFile(path)
	s := strings.Replace(string(b), `"name": "work"`, `"name": "work2"`, 1)
	s = strings.Replace(s, `"name": "home"`, `"name": "work"`, 1)
	os.WriteFile(path, []byte(s), 0o600)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = dir
	got := map[string]string{}
	for _, tn := range New(cfg, Options{}).list() {
		got[tn.cfg.Name] = filepath.Base(tn.srv.Dir)
	}
	if got["work2"] != "work" || got["work"] != "home" {
		t.Fatalf("state dirs after rename: %v", got)
	}
}

func TestNewStateSkipsOldLogins(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "work"), 0o700) // a removed tailnet's login
	c := &Config{Tailnets: []TailnetConfig{{Name: "other", State: "home"}}}
	for name, want := range map[string]string{
		"work": "work-2", // a leftover login
		"home": "home-2", // another tailnet's state
		"lab":  "lab",
	} {
		if s, err := c.NewState(dir, name); s != want || err != nil {
			t.Errorf("%s: %q %v, want %q", name, s, err, want)
		}
	}
	if _, err := c.NewState(dir, "../x"); err == nil {
		t.Error("a name leaving state_dir: no error")
	}
	// An unreadable state_dir is an error, not an endless search.
	if _, err := c.NewState(filepath.Join(dir, "\x00"), "x"); err == nil {
		t.Error("unreadable state_dir: no error")
	}
}

func TestNormalizeState(t *testing.T) {
	c := &Config{Tailnets: []TailnetConfig{{Name: "a"}, {Name: "b", State: "A"}}}
	if err := c.Normalize(); err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("shared state: %v", err)
	}
	c = &Config{Tailnets: []TailnetConfig{{Name: "a", State: "../x"}}}
	if err := c.Normalize(); err == nil {
		t.Fatal("state outside state_dir accepted")
	}
	c = &Config{Tailnets: []TailnetConfig{{Name: "Work"}}}
	if err := c.Normalize(); err != nil || c.Tailnets[0].State != "work" {
		t.Fatalf("default state: %+v %v", c.Tailnets[0], err)
	}
}
