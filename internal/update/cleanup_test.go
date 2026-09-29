package update

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestCleanupOldKegs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew paths and symlinks")
	}
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "tailmux")
	for _, v := range []string{"0.1.0", "0.1.1", "0.1.2", "0.1.3"} {
		os.MkdirAll(filepath.Join(cellar, v, "bin"), 0o755)
		os.WriteFile(filepath.Join(cellar, v, "bin", "tailmux"), nil, 0o755)
	}
	os.MkdirAll(filepath.Join(prefix, "opt"), 0o755)
	os.Symlink("../Cellar/tailmux/0.1.3", filepath.Join(prefix, "opt", "tailmux")) // just upgraded
	running := filepath.Join(cellar, "0.1.2", "bin", "tailmux")                    // not restarted yet

	list := func() []string {
		ents, _ := os.ReadDir(cellar)
		var n []string
		for _, e := range ents {
			n = append(n, e.Name())
		}
		return n
	}

	isRoot = func() bool { return false }
	CleanupOldKegs(running, t.Logf)
	if got := list(); len(got) != 4 {
		t.Fatalf("non-root must not touch anything: %v", got)
	}

	isRoot = func() bool { return true }
	t.Cleanup(func() { isRoot = func() bool { return os.Geteuid() == 0 } })
	if err := CleanupOldKegs(running, t.Logf); err != nil {
		t.Fatal(err)
	}
	if got := list(); !slices.Equal(got, []string{"0.1.2", "0.1.3"}) {
		t.Fatalf("kept %v, want the running and the linked version", got)
	}
	if err := CleanupOldKegs("/usr/local/bin/tailmux", t.Logf); err != nil {
		t.Fatalf("non-brew install: %v", err)
	}
}
