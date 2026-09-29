//go:build !windows

package tun

import "os"

func isAdmin() bool { return os.Geteuid() == 0 }
