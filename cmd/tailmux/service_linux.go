package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	linuxBinary = "/usr/local/bin/tailmux"
	linuxConfig = "/etc/tailmux/config.json"
	linuxState  = "/var/lib/tailmux"
	linuxUnit   = "/etc/systemd/system/tailmux.service"
)

func systemConfigPath() string { return linuxConfig }

func privileged() bool { return os.Geteuid() == 0 }

// invokingUserConfig is the personal config of whoever ran sudo/pkexec.
func invokingUserConfig() string {
	var u *user.User
	if uid := os.Getenv("PKEXEC_UID"); uid != "" {
		u, _ = user.LookupId(uid)
	} else if name := os.Getenv("SUDO_USER"); name != "" {
		u, _ = user.Lookup(name)
	}
	if u == nil {
		return ""
	}
	return filepath.Join(u.HomeDir, ".config", "tailmux", "config.json")
}

func serviceInstall() error {
	if !privileged() {
		return fmt.Errorf("run as root: sudo tailmux service install")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemd not found; run `sudo tailmux up -tun` from your init system instead")
	}
	// Package-managed copies stay where the package put them.
	bin, err := os.Executable()
	if err != nil || !strings.HasPrefix(bin, "/usr/bin/") {
		if bin, err = installBinary(linuxBinary); err != nil {
			return err
		}
	}
	if err := seedSystemConfig(linuxConfig, invokingUserConfig()); err != nil {
		return err
	}
	os.MkdirAll(linuxState, 0o700)
	unit := fmt.Sprintf(`[Unit]
Description=tailmux: all your tailnets at once
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=%s up -config %s -state-dir %s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, bin, linuxConfig, linuxState)
	if err := os.WriteFile(linuxUnit, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "tailmux"}, {"restart", "tailmux"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	fmt.Printf("tailmux is running as a service (config %s)\n", linuxConfig)
	return nil
}

func serviceUninstall() error {
	if !privileged() {
		return fmt.Errorf("run as root: sudo tailmux service uninstall")
	}
	exec.Command("systemctl", "disable", "--now", "tailmux").Run()
	os.Remove(linuxUnit)
	exec.Command("systemctl", "daemon-reload").Run()
	fmt.Printf("service removed; %s and %s are kept\n", linuxConfig, linuxState)
	return nil
}

func serviceStatus() error {
	if _, err := os.Stat(linuxUnit); err != nil {
		fmt.Println("not installed")
		return nil
	}
	out, _ := exec.Command("systemctl", "is-active", "tailmux").Output()
	fmt.Printf("installed, %s\n", strings.TrimSpace(string(out)))
	return nil
}

func replaceFile(tmp, dst string) error { return os.Rename(tmp, dst) }
