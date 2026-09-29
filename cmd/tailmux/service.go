package main

import "fmt"

// `tailmux service install|uninstall|status` sets tailmux up as a system
// service on Linux (systemd) and Windows (the service manager), running
// as root/SYSTEM so TUN mode works. The desktop apps run it elevated.
// macOS uses Homebrew's services instead.

func runService(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "install":
		return serviceInstall()
	case "uninstall":
		return serviceUninstall()
	case "status":
		return serviceStatus()
	}
	return fmt.Errorf("usage: tailmux service install|uninstall|status")
}
