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
		if target, _ := os.Readlink(dst); !strings.HasSuffix(target, "tailmux/"+appName) {
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

	// Copy next to it, then swap, so the app is never half there.
	tmp := dst + ".tailmux-new"
	os.RemoveAll(tmp)
	if out, err := exec.Command("/usr/bin/ditto", src, tmp).CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
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
	old := dst + ".tailmux-old"
	os.RemoveAll(old)
	if _, err := os.Lstat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			os.RemoveAll(tmp)
			return "", false, err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return "", false, err
	}
	os.RemoveAll(old)
	// Tell Launch Services (Spotlight, Launchpad, Finder) about it now.
	exec.Command("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-f", dst).Run()
	return dst, true, nil
}

func plistValue(app, key string) string {
	out, err := exec.Command("/usr/bin/defaults", "read", filepath.Join(app, "Contents", "Info"), key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
