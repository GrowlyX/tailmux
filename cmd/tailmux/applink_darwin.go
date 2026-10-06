package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Homebrew builds TailmuxBar.app into the formula's prefix, where Finder,
// Spotlight and Launchpad never look, and a formula can't install into
// /Applications itself (its post-install step is sandboxed to the
// prefix). A symlink there isn't enough either: Spotlight and Launchpad
// skip symlinks. So tailmux keeps a real copy in /Applications:
// `tailmux setup` and `tailmux bar` create it, and the daemon refreshes
// it whenever it starts on a new version, which is after every upgrade.

const (
	appName     = "TailmuxBar.app"
	appBundleID = "com.github.growlyx.tailmux.bar"
)

// Where the copy goes, and where it comes from; tests point these
// elsewhere.
var (
	applicationsDir = "/Applications"
	appSource       = bundledApp
)

// bundledApp is the TailmuxBar.app installed next to this binary
// (Homebrew's prefix), or "".
func bundledApp() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return ""
	}
	app := filepath.Join(filepath.Dir(exe), "..", appName)
	if _, err := os.Stat(filepath.Join(app, "Contents", "Info.plist")); err != nil {
		return ""
	}
	return filepath.Clean(app)
}

// syncApplicationsApp copies the bundled app to /Applications when the
// copy there is missing (only if create), a symlink to a tailmux keg, or
// another version of ours. It returns the path of the copy ("" when
// there's none to use) and whether it just wrote it.
// TAILMUX_NO_APPLICATIONS=1 turns it off.
func syncApplicationsApp(create bool) (string, bool, error) {
	src := appSource()
	if src == "" || os.Getenv("TAILMUX_NO_APPLICATIONS") != "" {
		return "", false, nil
	}
	// `tailmux bar`, `tailmux setup` and the daemon can all get here at
	// once; hold a lock on the folder itself from the check to the swap.
	dir, err := os.Open(applicationsDir)
	if err != nil {
		return "", false, err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return "", false, err
	}
	dst := filepath.Join(applicationsDir, appName)
	fi, err := os.Lstat(dst)
	switch {
	case os.IsNotExist(err):
		if !create {
			return "", false, nil
		}
	case err != nil:
		return "", false, err
	case fi.Mode()&os.ModeSymlink != 0:
		// The link the old caveats suggested: replace it with a copy.
		if target, _ := os.Readlink(dst); !homebrewLink(target, src) {
			return "", false, nil // someone else's link
		}
	default:
		if plistValue(dst, "CFBundleIdentifier") != appBundleID {
			return "", false, nil // not ours
		}
		if plistValue(dst, "CFBundleShortVersionString") == plistValue(src, "CFBundleShortVersionString") {
			return dst, false, nil
		}
	}

	// Copy into a private folder next to it, then swap, so the app is
	// never half there. Under the lock, any other staging folder is left
	// over from a crash.
	stale, _ := filepath.Glob(filepath.Join(applicationsDir, ".tailmux-*"))
	for _, p := range stale {
		os.RemoveAll(p)
	}
	stage, err := os.MkdirTemp(applicationsDir, ".tailmux-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(stage)
	tmp := filepath.Join(stage, appName)
	if out, err := exec.Command("/usr/bin/ditto", src, tmp).CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("copy %s: %v: %s", appName, err, strings.TrimSpace(string(out)))
	}
	// The daemon runs as root under `sudo brew services`; the copy should
	// belong to whoever owns the Homebrew install, like the app it copies.
	if os.Geteuid() == 0 {
		if st, err := os.Stat(src); err == nil {
			uid, gid := int(st.Sys().(*syscall.Stat_t).Uid), int(st.Sys().(*syscall.Stat_t).Gid)
			filepath.Walk(tmp, func(p string, _ os.FileInfo, _ error) error { return os.Lchown(p, uid, gid) })
		}
	}
	old := filepath.Join(stage, "old.app")
	if _, err := os.Lstat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return "", false, err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return "", false, err
	}
	// Tell Launch Services (Spotlight, Launchpad, Finder) about it now.
	exec.Command("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-f", dst).Run()
	return dst, true, nil
}

// homebrewLink reports whether target, a symlink's destination, is this
// Homebrew install's app: <prefix>/opt/tailmux/TailmuxBar.app, or one in
// <prefix>/Cellar/tailmux/<version>. src is the app in the running keg.
func homebrewLink(target, src string) bool {
	formula := filepath.Dir(filepath.Dir(src)) // <prefix>/Cellar/tailmux
	if filepath.Base(formula) != "tailmux" || filepath.Base(filepath.Dir(formula)) != "Cellar" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(applicationsDir, target)
	}
	target = filepath.Clean(target)
	prefix := filepath.Dir(filepath.Dir(formula))
	if target == filepath.Join(prefix, "opt", "tailmux", appName) {
		return true
	}
	keg := filepath.Dir(target)
	return filepath.Base(target) == appName && filepath.Dir(keg) == formula
}

func plistValue(app, key string) string {
	out, err := exec.Command("/usr/bin/defaults", "read", filepath.Join(app, "Contents", "Info"), key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
