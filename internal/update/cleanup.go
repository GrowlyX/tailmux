package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isRoot is swapped out in tests.
var isRoot = func() bool { return os.Geteuid() == 0 }

// CleanupOldKegs removes old Homebrew versions of tailmux that brew
// itself can't. `sudo brew services start` makes the keg root-owned, so
// the unprivileged `brew upgrade` behind every later update can't delete
// it and warns instead. The root daemon can: it deletes every
// Cellar/tailmux/<version> except the one it runs from and the one
// opt/tailmux points at, which is exactly what `brew cleanup` would do.
func CleanupOldKegs(exe string, logf func(string, ...any)) error {
	if !isRoot() {
		return nil
	}
	i := strings.Index(exe, "/Cellar/tailmux/")
	if i < 0 {
		return nil
	}
	prefix, cellar := exe[:i], exe[:i+len("/Cellar/tailmux")]
	keep := map[string]bool{}
	if rel, err := filepath.Rel(cellar, exe); err == nil {
		keep[strings.SplitN(rel, string(filepath.Separator), 2)[0]] = true
	}
	if opt, err := filepath.EvalSymlinks(filepath.Join(prefix, "opt", "tailmux")); err == nil {
		keep[filepath.Base(opt)] = true
	}
	ents, err := os.ReadDir(cellar)
	if err != nil {
		return err
	}
	var errs []string
	for _, e := range ents {
		if !e.IsDir() || keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(cellar, e.Name())); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		logf("removed old version %s", e.Name())
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup: %s", strings.Join(errs, "; "))
	}
	return nil
}
