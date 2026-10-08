// Package update keeps tailmux current: it checks GitHub releases,
// installs a newer one the same way this copy was installed (Homebrew,
// or a checksum-verified binary swap), and lets the daemon restart
// itself when its executable changes underneath it.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// ReleasesAPI is the latest-release endpoint; TAILMUX_RELEASES_API
// overrides it (tests, mirrors).
var ReleasesAPI = envOr("TAILMUX_RELEASES_API", "https://api.github.com/repos/GrowlyX/tailmux/releases/latest")

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Formula is what `brew upgrade` is asked for.
const Formula = "growlyx/tap/tailmux"

type Release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Assets []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Version is the release version without the leading v.
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

var client = &http.Client{Timeout: 60 * time.Second}

// Latest fetches the newest published release.
func Latest(ctx context.Context) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", ReleasesAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("releases: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !semver.IsValid(r.Tag) {
		return nil, fmt.Errorf("releases: bad tag %q", r.Tag)
	}
	return &r, nil
}

// Newer reports whether latest is a newer release than current. Dev
// builds ("dev", commit hashes) never update themselves.
func Newer(current, latest string) bool {
	c, l := "v"+strings.TrimPrefix(current, "v"), "v"+strings.TrimPrefix(latest, "v")
	if !semver.IsValid(c) || !semver.IsValid(l) {
		return false
	}
	if semver.MajorMinor(c) == "v0.0" && semver.Compare(c, "v0.0.1") < 0 {
		return false // 0.0.0-something: a CI or local build, never replaced
	}
	return semver.Compare(l, c) > 0
}

type Method string

const (
	MethodBrew   Method = "homebrew"
	MethodBinary Method = "binary"
	MethodNone   Method = "unsupported"
)

// Detect works out how exe (a resolved path) was installed.
func Detect(exe string) (m Method, brew string) {
	if i := strings.Index(exe, "/Cellar/tailmux/"); i > 0 {
		brew = filepath.Join(exe[:i], "bin", "brew")
		if _, err := os.Stat(brew); err == nil {
			return MethodBrew, brew
		}
	}
	if strings.Contains(exe, "go-build") || strings.HasPrefix(filepath.Base(exe), "__debug") {
		return MethodNone, "" // go run / debugger
	}
	if strings.HasPrefix(exe, "/usr/bin/") {
		return MethodNone, "" // a distro package: its package manager updates it
	}
	return MethodBinary, ""
}

// Install installs rel over the executable at exe. Progress goes to logf.
func Install(ctx context.Context, rel *Release, exe string, logf func(string, ...any)) error {
	switch m, brew := Detect(exe); m {
	case MethodBrew:
		logf("updating with Homebrew (builds from source, takes a minute or two)")
		return runBrew(ctx, brew, "upgrade", Formula)
	case MethodBinary:
		return installBinary(ctx, rel, exe, logf)
	default:
		return errors.New("this copy can't update itself (a dev build, or installed by a package manager: update it there)")
	}
}

// brewEnv makes `brew upgrade` refresh the tap first (a tap-qualified
// formula triggers brew's own tap auto-update; 1s means always) instead
// of a separate `brew update`, which updates every tap and core too.
var brewEnv = []string{"HOMEBREW_AUTO_UPDATE_SECS=1", "HOMEBREW_NO_ENV_HINTS=1"}

// runBrew runs brew as the user who owns the Homebrew prefix. Homebrew
// refuses to run as root, and the TUN daemon is root.
func runBrew(ctx context.Context, brew string, args ...string) error {
	cmd := exec.CommandContext(ctx, brew, args...)
	cmd.Env = append(os.Environ(), brewEnv...)
	if os.Geteuid() == 0 {
		u, err := brewUser(brew)
		if err != nil {
			return err
		}
		// sudo resets the environment, so brew's settings go through env.
		argv := append([]string{"-u", u.Username, "-H", "--", "env"}, brewEnv...)
		cmd = exec.CommandContext(ctx, "sudo", append(append(argv, brew), args...)...)
		cmd.Dir = u.HomeDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("brew %s: %v: %s", strings.Join(args, " "), err, lastLines(string(out), 6))
	}
	return nil
}

// brewUser picks the non-root user to run brew as: whoever owns brew,
// the prefix, or the Cellar, else whoever is logged in at the console.
// bin/brew alone isn't enough: it can end up root-owned, and running
// "as" root is exactly what Homebrew refuses.
func brewUser(brew string) (*user.User, error) {
	prefix := filepath.Dir(filepath.Dir(brew))
	for _, p := range []string{brew, prefix, filepath.Join(prefix, "Cellar"), "/dev/console"} {
		if uid, err := ownerUID(p); err == nil && uid != 0 {
			return user.LookupId(strconv.Itoa(uid))
		}
	}
	return nil, fmt.Errorf("%s is owned by root and nobody is logged in: run `brew upgrade %s` as the user who installed Homebrew", prefix, Formula)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// installBinary downloads the release archive for this platform, checks
// it against checksums.txt, and atomically replaces exe.
func installBinary(ctx context.Context, rel *Release, exe string, logf func(string, ...any)) error {
	ext, binName := "tar.gz", "tailmux"
	if runtime.GOOS == "windows" {
		ext, binName = "zip", "tailmux.exe"
	}
	name := fmt.Sprintf("tailmux_%s_%s_%s.%s", rel.Version(), runtime.GOOS, runtime.GOARCH, ext)
	var archiveURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case name:
			archiveURL = a.URL
		case "checksums.txt":
			sumsURL = a.URL
		}
	}
	if archiveURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s or checksums.txt", rel.Tag, name)
	}
	sums, err := fetch(ctx, sumsURL)
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	logf("downloading %s", name)
	archive, err := fetch(ctx, archiveURL)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: checksum mismatch", name)
	}
	bin, err := extract(archive, binName)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("write %s (need sudo?): %w", tmp, err)
	}
	if runtime.GOOS == "windows" {
		// A running .exe can't be overwritten, only renamed aside.
		os.Remove(exe + ".old")
		if err := os.Rename(exe, exe+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	logf("installed %s to %s", rel.Tag, exe)
	return nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

func extract(archive []byte, want string) ([]byte, error) {
	if bytes.HasPrefix(archive, []byte("PK\x03\x04")) {
		return extractZip(archive, want)
	}
	zr, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("archive has no %s", want)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == want && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
}

func extractZip(archive []byte, want string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != want || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 256<<20))
	}
	return nil, fmt.Errorf("archive has no %s", want)
}
