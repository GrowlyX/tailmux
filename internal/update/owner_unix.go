//go:build unix

package update

import (
	"os"
	"syscall"
)

func ownerUID(path string) (int, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return int(st.Sys().(*syscall.Stat_t).Uid), nil
}

// Reexec replaces this process with a fresh start of path.
func Reexec(path string) error { return syscall.Exec(path, os.Args, os.Environ()) }
