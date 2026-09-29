//go:build windows

package tun

import "golang.org/x/sys/windows"

func isAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }
