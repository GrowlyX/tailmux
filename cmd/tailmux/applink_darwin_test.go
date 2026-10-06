package main

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeApp(t *testing.T, dir, id, version string) string {
	t.Helper()
	app := filepath.Join(dir, appName)
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + id + `</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestSyncApplicationsApp(t *testing.T) {
	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Cellar", "tailmux", "1.1.0")
	src := fakeApp(t, keg, appBundleID, "1.1.0")
	applicationsDir = t.TempDir()
	appSource = func() string { return src }
	t.Cleanup(func() { applicationsDir, appSource = "/Applications", bundledApp })
	dst := filepath.Join(applicationsDir, appName)
	version := func() string { return plistValue(dst, "CFBundleShortVersionString") }

	// The daemon only refreshes a copy that's there.
	if path, wrote, err := syncApplicationsApp(false); path != "" || wrote || err != nil {
		t.Fatalf("no copy, create=false: %q %v %v", path, wrote, err)
	}
	if path, wrote, err := syncApplicationsApp(true); path != dst || !wrote || err != nil || version() != "1.1.0" {
		t.Fatalf("create: %q %v %v, version %q", path, wrote, err, version())
	}
	if _, wrote, _ := syncApplicationsApp(true); wrote {
		t.Fatal("same version copied again")
	}

	// An upgrade: the daemon brings the copy along.
	src = fakeApp(t, filepath.Join(filepath.Dir(keg), "1.2.0"), appBundleID, "1.2.0")
	if _, wrote, err := syncApplicationsApp(false); !wrote || err != nil || version() != "1.2.0" {
		t.Fatalf("upgrade: %v %v, version %q", wrote, err, version())
	}
	if m, _ := filepath.Glob(filepath.Join(applicationsDir, ".tailmux-*")); len(m) > 0 {
		t.Fatalf("left behind: %v", m)
	}

	// The symlink the old caveats suggested becomes a copy.
	os.RemoveAll(dst)
	for _, target := range []string{filepath.Join(prefix, "opt", "tailmux", appName), filepath.Join(keg, appName)} {
		os.RemoveAll(dst)
		os.Symlink(target, dst)
		if _, wrote, err := syncApplicationsApp(false); !wrote || err != nil {
			t.Fatalf("symlink to %s: %v %v", target, wrote, err)
		}
		if fi, _ := os.Lstat(dst); fi.Mode()&os.ModeSymlink != 0 {
			t.Fatal("still a symlink")
		}
	}

	// Someone else's app or link is left alone.
	os.RemoveAll(dst)
	fakeApp(t, applicationsDir, "com.example.other", "9.9")
	if _, wrote, _ := syncApplicationsApp(true); wrote {
		t.Fatal("replaced someone else's app")
	}
	os.RemoveAll(dst)
	for _, target := range []string{"/somewhere/else.app", "/Users/alice/not-tailmux/" + appName, "/opt/homebrew/opt/tailmux/" + appName} {
		os.RemoveAll(dst)
		os.Symlink(target, dst)
		if _, wrote, _ := syncApplicationsApp(true); wrote {
			t.Fatalf("replaced someone else's link to %s", target)
		}
	}

	t.Setenv("TAILMUX_NO_APPLICATIONS", "1")
	os.RemoveAll(dst)
	if path, _, _ := syncApplicationsApp(true); path != "" {
		t.Fatal("TAILMUX_NO_APPLICATIONS ignored")
	}
}

func TestSyncApplicationsAppConcurrent(t *testing.T) {
	keg := filepath.Join(t.TempDir(), "Cellar", "tailmux", "1.1.0")
	src := fakeApp(t, keg, appBundleID, "1.1.0")
	applicationsDir = t.TempDir()
	appSource = func() string { return src }
	t.Cleanup(func() { applicationsDir, appSource = "/Applications", bundledApp })
	dst := fakeApp(t, applicationsDir, appBundleID, "1.0.0")

	// `tailmux bar` and the daemon refreshing an old copy at the same time.
	type result struct {
		wrote bool
		err   error
	}
	results := make(chan result, 8)
	for range 8 {
		go func() { _, wrote, err := syncApplicationsApp(true); results <- result{wrote, err} }()
	}
	writes := 0
	for range 8 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.wrote {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("copied %d times, want once", writes)
	}
	if v := plistValue(dst, "CFBundleShortVersionString"); v != "1.1.0" {
		t.Fatalf("version %q after concurrent syncs", v)
	}
	if m, _ := filepath.Glob(filepath.Join(applicationsDir, ".tailmux-*")); len(m) > 0 {
		t.Fatalf("left behind: %v", m)
	}
}
