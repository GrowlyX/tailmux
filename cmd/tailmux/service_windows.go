package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "tailmux"

func programData() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return p
	}
	return `C:\ProgramData`
}

func programFiles() string {
	if p := os.Getenv("ProgramFiles"); p != "" {
		return p
	}
	return `C:\Program Files`
}

func systemConfigPath() string { return filepath.Join(programData(), "tailmux", "config.json") }
func systemStateDir() string   { return filepath.Join(programData(), "tailmux", "state") }

// privileged: TUN mode needs an elevated token (Administrator or SYSTEM).
func privileged() bool { return windows.GetCurrentProcessToken().IsElevated() }

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// runAsService runs `up` under the service manager: Stop and Shutdown
// cancel it. After a self-update `up` returns errRestart; exiting with an
// error then makes the service manager's recovery action start the new
// binary.
func runAsService(run func(context.Context) error) error {
	var runErr error
	err := svc.Run(serviceName, handler(func(ctx context.Context) error {
		runErr = run(ctx)
		return runErr
	}))
	if err != nil {
		return err
	}
	var re errRestart
	if errors.As(runErr, &re) {
		os.Exit(3) // recovery restarts us on the new binary
	}
	return runErr
}

type handler func(context.Context) error

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func serviceInstall() error {
	if !privileged() {
		return fmt.Errorf("run from an Administrator prompt: tailmux service install")
	}
	dir := filepath.Join(programFiles(), "tailmux")
	bin, err := installBinary(filepath.Join(dir, "tailmux.exe"))
	if err != nil {
		return err
	}
	// Wintun ships next to the executable; bring it along.
	if src, err := os.Executable(); err == nil {
		dll := filepath.Join(filepath.Dir(src), "wintun.dll")
		if _, err := os.Stat(dll); err == nil && filepath.Dir(src) != dir {
			if err := copyFile(dll, filepath.Join(dir, "wintun.dll"), 0o644); err != nil {
				return err
			}
		}
	}
	home, _ := os.UserHomeDir()
	if err := seedSystemConfig(systemConfigPath(), filepath.Join(home, ".config", "tailmux", "config.json")); err != nil {
		return err
	}
	os.MkdirAll(systemStateDir(), 0o700)

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	args := []string{"up", "-config", systemConfigPath(), "-state-dir", systemStateDir()}
	s, err := m.OpenService(serviceName)
	if err == nil {
		// Reinstall: stop the old one and point it at the new binary.
		s.Control(svc.Stop)
		waitStopped(s)
		cfg, _ := s.Config()
		cfg.BinaryPathName = syscallQuote(bin, args)
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return err
		}
	} else {
		s, err = m.CreateService(serviceName, bin, mgr.Config{
			DisplayName: "tailmux",
			Description: "Be on all your tailnets at once.",
			StartType:   mgr.StartAutomatic,
		}, args...)
		if err != nil {
			return err
		}
	}
	defer s.Close()
	// Restart after crashes and after self-updates (which exit non-zero).
	s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 3600)
	s.SetRecoveryActionsOnNonCrashFailures(true)
	if err := s.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	fmt.Printf("tailmux is running as a service (config %s)\n", systemConfigPath())
	return nil
}

func syscallQuote(bin string, args []string) string {
	s := windows.EscapeArg(bin)
	for _, a := range args {
		s += " " + windows.EscapeArg(a)
	}
	return s
}

func waitStopped(s *mgr.Service) {
	for range 60 {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func serviceUninstall() error {
	if !privileged() {
		return fmt.Errorf("run from an Administrator prompt: tailmux service uninstall")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("not installed")
		return nil
	}
	defer s.Close()
	s.Control(svc.Stop)
	waitStopped(s)
	if err := s.Delete(); err != nil {
		return err
	}
	fmt.Printf("service removed; %s is kept\n", filepath.Dir(systemConfigPath()))
	return nil
}

func serviceStatus() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("not installed")
		return nil
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return err
	}
	state := map[svc.State]string{svc.Running: "running", svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping"}[st.State]
	fmt.Printf("installed, %s\n", state)
	return nil
}

// replaceFile swaps dst for tmp. Windows can't overwrite a running
// executable, but it can rename it out of the way.
func replaceFile(tmp, dst string) error {
	os.Remove(dst + ".old")
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, dst+".old"); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}
