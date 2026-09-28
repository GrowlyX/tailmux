package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func build(t *testing.T, out, version string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, "-ldflags", "-X main.version="+version, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, b)
	}
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestSelfUpdate runs a real v0.0.1 daemon next to a fake release server
// offering v0.0.2, and waits for the daemon to install it, notice its
// binary changed, and re-exec as v0.0.2, all on its own.
func TestSelfUpdate(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("builds binaries")
	}
	dir := t.TempDir()
	installed := filepath.Join(dir, "bin", "tailmux")
	os.MkdirAll(filepath.Dir(installed), 0o755)
	build(t, installed, "0.0.1")
	next := filepath.Join(dir, "next", "tailmux")
	build(t, next, "0.0.2")

	// Release server.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body, _ := os.ReadFile(next)
	tw.WriteHeader(&tar.Header{Name: "tailmux", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	zw.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	name := fmt.Sprintf("tailmux_0.0.2_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.0.2","html_url":"https://example/v0.0.2","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
				name, srv.URL+"/a", srv.URL+"/sums")
		case "/a":
			w.Write(archive)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		}
	}))
	defer srv.Close()

	api := freePort(t)
	cfgPath := filepath.Join(dir, "config.json")
	cfg := fmt.Sprintf(`{"state_dir":%q,"socks5":%q,"http":%q,"tailnets":[{"name":"offline","control_url":"http://127.0.0.1:9","ephemeral":true}]}`,
		filepath.Join(dir, "state"), freePort(t), api)
	os.WriteFile(cfgPath, []byte(cfg), 0o600)

	cmd := exec.Command(installed, "up", "-config", cfgPath)
	cmd.Env = append(os.Environ(), "TAILMUX_RELEASES_API="+srv.URL+"/latest", "TAILMUX_UPDATE_DELAY=1s")
	logf, _ := os.Create(filepath.Join(dir, "daemon.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	version := func() string {
		resp, err := http.Get("http://" + api + "/status")
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		var s struct {
			Version string `json:"version"`
		}
		json.NewDecoder(resp.Body).Decode(&s)
		return s.Version
	}
	deadline := time.Now().Add(2 * time.Minute)
	seen := map[string]bool{}
	for time.Now().Before(deadline) {
		v := version()
		seen[v] = true
		if v == "0.0.2" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !seen["0.0.1"] || !seen["0.0.2"] {
		log, _ := os.ReadFile(filepath.Join(dir, "daemon.log"))
		t.Fatalf("versions seen %v, want 0.0.1 then 0.0.2\n%s", seen, log)
	}
	if out, _ := exec.Command(installed, "version").Output(); string(out) != "tailmux 0.0.2\n" {
		t.Fatalf("installed binary reports %q", out)
	}
	// Same process, re-exec'd: the one we started is still alive.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("daemon gone: %v", err)
	}
}
