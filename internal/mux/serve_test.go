package mux

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"tailscale.com/ipn"
)

const (
	oldHost = "box.old.ts.net"
	newHost = "box.example.ts.net"
)

// served lists what sc forwards, as "port -> target" and
// "host:port/path -> target", sorted.
func served(sc *ipn.ServeConfig) []string {
	var out []string
	for port, h := range sc.TCP {
		if h.TCPForward != "" {
			out = append(out, strconv.Itoa(int(port))+" -> "+h.TCPForward+map[bool]string{true: " tls"}[h.TerminateTLS != ""])
		}
	}
	for hp, w := range sc.Web {
		for mount, h := range w.Handlers {
			out = append(out, string(hp)+mount+" -> "+h.Proxy)
		}
	}
	slices.Sort(out)
	return out
}

func TestApplyServes(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(sc *ipn.ServeConfig)
		prev    serveRecord
		entries []ServeEntry
		host    string
		want    []string
		record  serveRecord
	}{
		{
			name:    "hand-added port kept",
			setup:   func(sc *ipn.ServeConfig) { sc.SetTCPForwarding(22, "127.0.0.1:22", false, 0, newHost) },
			entries: []ServeEntry{{Port: 3773, TCP: "127.0.0.1:3773"}},
			host:    newHost,
			want:    []string{"22 -> 127.0.0.1:22", "3773 -> 127.0.0.1:3773"},
			record:  serveRecord{TCP: []uint16{3773}},
		},
		{
			name:   "owned port dropped from config is removed",
			setup:  func(sc *ipn.ServeConfig) { sc.SetTCPForwarding(3773, "127.0.0.1:3773", false, 0, newHost) },
			prev:   serveRecord{TCP: []uint16{3773}},
			host:   newHost,
			want:   nil,
			record: serveRecord{},
		},
		{
			name:    "config overwrites a hand-added entry on its port",
			setup:   func(sc *ipn.ServeConfig) { sc.SetTCPForwarding(3773, "127.0.0.1:9999", false, 0, newHost) },
			entries: []ServeEntry{{Port: 3773, TLSTCP: "127.0.0.1:3773"}},
			host:    newHost,
			want:    []string{"3773 -> 127.0.0.1:3773 tls"},
			record:  serveRecord{TCP: []uint16{3773}},
		},
		{
			name: "tcp entry replaces hand-added web handler",
			setup: func(sc *ipn.ServeConfig) {
				sc.SetWebHandler(&ipn.HTTPHandler{Proxy: "http://127.0.0.1:8080"}, newHost, 80, "/", false, "")
			},
			entries: []ServeEntry{{Port: 80, TCP: "127.0.0.1:8081"}},
			host:    newHost,
			want:    []string{"80 -> 127.0.0.1:8081"},
			record:  serveRecord{TCP: []uint16{80}},
		},
		{
			name: "config drops hand-added paths on its web port",
			setup: func(sc *ipn.ServeConfig) {
				sc.SetWebHandler(&ipn.HTTPHandler{Proxy: "http://127.0.0.1:7000"}, newHost, 443, "/admin", true, "")
				sc.SetWebHandler(&ipn.HTTPHandler{Proxy: "http://127.0.0.1:8000"}, newHost, 8443, "/admin", true, "")
			},
			entries: []ServeEntry{{Port: 443, HTTPS: "http://127.0.0.1:3000", Path: "/"}},
			host:    newHost,
			want: []string{
				newHost + ":443/ -> http://127.0.0.1:3000",
				newHost + ":8443/admin -> http://127.0.0.1:8000",
			},
			record: serveRecord{Web: map[ipn.HostPort][]string{newHost + ":443": {"/"}}},
		},
		{
			name: "DNS name change moves web handlers",
			setup: func(sc *ipn.ServeConfig) {
				sc.SetWebHandler(&ipn.HTTPHandler{Proxy: "http://127.0.0.1:3000"}, oldHost, 443, "/", true, "")
			},
			prev:    serveRecord{Web: map[ipn.HostPort][]string{oldHost + ":443": {"/"}}},
			entries: []ServeEntry{{Port: 443, HTTPS: "http://127.0.0.1:3000", Path: "/"}},
			host:    newHost,
			want:    []string{newHost + ":443/ -> http://127.0.0.1:3000"},
			record:  serveRecord{Web: map[ipn.HostPort][]string{newHost + ":443": {"/"}}},
		},
		{
			name: "https mounts on one port coexist",
			entries: []ServeEntry{
				{Port: 443, HTTPS: "http://127.0.0.1:3000", Path: "/"},
				{Port: 443, HTTPS: "http://127.0.0.1:9000", Path: "/api"},
			},
			host: newHost,
			want: []string{
				newHost + ":443/ -> http://127.0.0.1:3000",
				newHost + ":443/api -> http://127.0.0.1:9000",
			},
			record: serveRecord{Web: map[ipn.HostPort][]string{newHost + ":443": {"/", "/api"}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := new(ipn.ServeConfig)
			if c.setup != nil {
				c.setup(sc)
			}
			rec := applyServes(sc, c.prev, c.entries, c.host)
			if got := served(sc); !slices.Equal(got, c.want) {
				t.Errorf("served %q, want %q", got, c.want)
			}
			if !slices.Equal(rec.TCP, c.record.TCP) || len(rec.Web) != len(c.record.Web) {
				t.Fatalf("record %+v, want %+v", rec, c.record)
			}
			for hp, mounts := range c.record.Web {
				if !slices.Equal(rec.Web[hp], mounts) {
					t.Errorf("record %s: %q, want %q", hp, rec.Web[hp], mounts)
				}
			}
		})
	}
}

func TestNormalizeServes(t *testing.T) {
	expand := []struct {
		in   ServeEntry
		want ServeEntry
	}{
		{ServeEntry{Port: 3773, TCP: "3773"}, ServeEntry{Port: 3773, TCP: "127.0.0.1:3773"}},
		{ServeEntry{Port: 5432, TLSTCP: "tcp://db:5432"}, ServeEntry{Port: 5432, TLSTCP: "db:5432"}},
		{ServeEntry{Port: 443, HTTPS: "3000"}, ServeEntry{Port: 443, HTTPS: "http://127.0.0.1:3000", Path: "/"}},
		{ServeEntry{Port: 80, HTTP: "localhost:8080", Path: "/x"}, ServeEntry{Port: 80, HTTP: "http://localhost:8080", Path: "/x"}},
		{ServeEntry{Port: 443, HTTPS: "https+insecure://127.0.0.1:8443"}, ServeEntry{Port: 443, HTTPS: "https+insecure://127.0.0.1:8443", Path: "/"}},
	}
	for _, c := range expand {
		got := []ServeEntry{c.in}
		if err := normalizeServes(got); err != nil || got[0] != c.want {
			t.Errorf("%+v: got %+v %v, want %+v", c.in, got[0], err, c.want)
		}
	}

	reject := map[string][]ServeEntry{
		"no port":              {{TCP: "1"}},
		"no target":            {{Port: 1}},
		"two targets":          {{Port: 1, TCP: "1", HTTP: "1"}},
		"tcp and web":          {{Port: 1, TCP: "1"}, {Port: 1, HTTP: "2", Path: "/a"}},
		"tcp twice":            {{Port: 1, TCP: "1"}, {Port: 1, TCP: "2"}},
		"http and https":       {{Port: 1, HTTP: "1"}, {Port: 1, HTTPS: "2", Path: "/a"}},
		"same path twice":      {{Port: 1, HTTP: "1"}, {Port: 1, HTTP: "2"}},
		"path on tcp":          {{Port: 1, TCP: "1", Path: "/"}},
		"relative path":        {{Port: 1, HTTP: "1", Path: "api"}},
		"bad tcp target":       {{Port: 1, TCP: "http://x:1"}},
		"bad web target":       {{Port: 1, HTTP: "ftp://x:1"}},
		"web target, no port":  {{Port: 1, HTTP: "x"}},
		"tcp target, bad port": {{Port: 1, TCP: "70000"}},
		"remote, no scheme":    {{Port: 1, TCP: "db:5432"}},
	}
	for name, entries := range reject {
		if err := normalizeServes(entries); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestConfigServeErrorNamesTailnet(t *testing.T) {
	c := &Config{Tailnets: []TailnetConfig{{Name: "home", Serve: []ServeEntry{{Port: 1}}}}}
	if err := c.Normalize(); err == nil || !strings.Contains(err.Error(), `"home"`) {
		t.Fatalf("got %v", err)
	}
}

// The setup TUI and apps edit the config through ReadConfig and Save;
// serve entries they don't know about must come through as written.
func TestServeSurvivesConfigEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"tailnets":[{"name":"home","serve":[{"port":443,"https":"3000","path":"/api"}]}]}`), 0o600)
	c, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c, err = ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []ServeEntry{{Port: 443, HTTPS: "3000", Path: "/api"}}
	if !slices.Equal(c.Tailnets[0].Serve, want) {
		t.Fatalf("serve after save: %+v", c.Tailnets[0].Serve)
	}
}

func TestServeRecordRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), serveRecordFile)
	want := serveRecord{TCP: []uint16{22}, Web: map[ipn.HostPort][]string{newHost + ":443": {"/"}}}
	if err := saveServeRecord(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadServeRecord(path)
	if err != nil || !slices.Equal(got.TCP, want.TCP) || !slices.Equal(got.Web[newHost+":443"], []string{"/"}) {
		t.Fatalf("got %+v %v", got, err)
	}
	if err := saveServeRecord(path, serveRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty record left a file: %v", err)
	}
}
