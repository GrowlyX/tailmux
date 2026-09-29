//go:build !linux && !windows

package main

import (
	"fmt"
	"os"
)

func systemConfigPath() string {
	if defaultConfig != "" {
		return defaultConfig
	}
	return "/usr/local/etc/tailmux/config.json"
}

func privileged() bool { return os.Geteuid() == 0 }

// On macOS, Homebrew runs the service.
func serviceInstall() error {
	return fmt.Errorf("on macOS use Homebrew: sudo brew services start tailmux")
}

func serviceUninstall() error {
	return fmt.Errorf("on macOS use Homebrew: sudo brew services stop tailmux")
}

func serviceStatus() error {
	fmt.Println("on macOS the service is managed by Homebrew: brew services list")
	return nil
}
