package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.0", "0.1.1", true},
		{"v0.1.0", "v0.2.0", true},
		{"0.1.1", "0.1.1", false},
		{"0.2.0", "0.1.9", false},
		{"dev", "0.9.0", false},
		{"0.1.0", "garbage", false},
		{"0.1.0", "0.1.1-rc.1", true},
		{"0.1.1-rc.1", "0.1.1", true},
	} {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.cur, c.latest, got)
		}
	}
}

func TestDetect(t *testing.T) {
	prefix := t.TempDir()
	os.MkdirAll(filepath.Join(prefix, "bin"), 0o755)
	os.WriteFile(filepath.Join(prefix, "bin", "brew"), nil, 0o755)
	exe := filepath.Join(prefix, "Cellar", "tailmux", "0.1.0", "bin", "tailmux")
	if m, brew := Detect(exe); m != MethodBrew || brew != filepath.Join(prefix, "bin", "brew") {
		t.Errorf("brew: %v %q", m, brew)
	}
	if m, _ := Detect("/usr/local/bin/tailmux"); m != MethodBinary {
		t.Errorf("binary: %v", m)
	}
	if m, _ := Detect("/private/var/folders/x/go-build123/b001/exe/tailmux"); m != MethodNone {
		t.Errorf("go run: %v", m)
	}
}

// fakeRelease serves a latest-release JSON, a tar.gz holding a
// "tailmux" file with body, and checksums.txt (optionally wrong).
func fakeRelease(t *testing.T, version string, body []byte, badSum bool) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("hi")}, {"tailmux", body}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		tw.Write(f.data)
	}
	tw.Close()
	zw.Close()
	archive := buf.Bytes()
	name := fmt.Sprintf("tailmux_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	sumHex := hex.EncodeToString(sum[:])
	if badSum {
		sumHex = hex.EncodeToString(make([]byte, 32))
	}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Tag: "v" + version, URL: "https://example/v" + version, Assets: []Asset{
			{Name: name, URL: srv.URL + "/archive"},
			{Name: "checksums.txt", URL: srv.URL + "/sums"},
		}})
	})
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "deadbeef  other.tar.gz\n%s  %s\n", sumHex, name)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := ReleasesAPI
	ReleasesAPI = srv.URL + "/latest"
	t.Cleanup(func() { ReleasesAPI = old })
	return srv
}

func TestInstallBinary(t *testing.T) {
	ctx := context.Background()
	exe := filepath.Join(t.TempDir(), "tailmux")
	os.WriteFile(exe, []byte("OLD"), 0o755)

	fakeRelease(t, "9.9.9", []byte("NEW"), true)
	rel, err := Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(ctx, rel, exe, t.Logf); err == nil {
		t.Fatal("bad checksum must be rejected")
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatal("exe changed despite bad checksum")
	}

	fakeRelease(t, "9.9.9", []byte("NEW"), false)
	rel, _ = Latest(ctx)
	if err := Install(ctx, rel, exe, t.Logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW" {
		t.Fatalf("exe = %q", b)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o111 == 0 {
		t.Fatal("not executable")
	}
}

func TestManager(t *testing.T) {
	fakeRelease(t, "0.2.0", []byte("NEW"), false)
	exe := filepath.Join(t.TempDir(), "tailmux")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	m := NewManager("0.1.0", true)
	m.Exe = exe
	st, err := m.Check(context.Background())
	if err != nil || !st.Available || st.Latest != "0.2.0" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Status().State != "installed" {
		t.Fatalf("%+v", m.Status())
	}
	if up := NewManager("0.2.0", true); func() bool { s, _ := up.Check(context.Background()); return s.Available }() {
		t.Fatal("same version should not be available")
	}
}

func TestWatchExecutable(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("a"), 0o755)
	os.WriteFile(b, []byte("bb"), 0o755)
	link := filepath.Join(dir, "tailmux")
	os.Symlink(a, link)
	fired := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go WatchExecutable(ctx, link, 20*time.Millisecond, func() { close(fired) })
	time.Sleep(100 * time.Millisecond)
	select {
	case <-fired:
		t.Fatal("fired without a change")
	default:
	}
	os.Remove(link)
	os.Symlink(b, link) // what `brew upgrade` does to opt/tailmux
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("did not notice the new binary")
	}
}
