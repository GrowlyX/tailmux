package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/GrowlyX/tailmux/internal/mux"
	"github.com/GrowlyX/tailmux/internal/tscli"
)

// runCLI is `tailmux cli [-profile name|*] <tailscale args>`: the
// tailscale CLI, built into tailmux, run against one tailnet's node, or
// each in turn.
func runCLI(cfgPath string, args []string) error {
	fs := flag.NewFlagSet("tailmux cli", flag.ContinueOnError)
	fs.StringVar(&cfgPath, "config", cfgPath, "config file")
	profile := fs.String("profile", "", "tailnet to run against, or '*' for each in turn (default: the only one)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: tailmux cli [-profile tailnet|'*'] <tailscale command> [args]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.Arg(0) == "ssh" {
		// tailscale ssh re-runs its own binary as the ProxyCommand and
		// replaces itself with ssh, which would take the socket with it.
		return errors.New("plain `ssh host` already reaches every tailnet through tailmux (or use `ProxyCommand tailmux nc %h %p`)")
	}
	cfg, err := mux.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	var s mux.Status
	if err := apiGet(cfg, "/status", &s); err != nil {
		return err
	}
	var names []string
	for _, t := range s.Tailnets {
		names = append(names, t.Name)
	}

	switch {
	case *profile == "*":
		return eachTailnet(cfgPath, names, fs.Args())
	case *profile == "" && len(names) == 1:
		*profile = names[0]
	case *profile == "":
		return fmt.Errorf("pick a tailnet with -profile: %s, or '*' for each", strings.Join(names, ", "))
	case !slices.Contains(names, *profile):
		return fmt.Errorf("no tailnet %q; have %s", *profile, strings.Join(names, ", "))
	}
	err = tscli.Run(context.Background(), cfg.HTTP, *profile, fs.Args())
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

// eachTailnet runs the command once per tailnet, each in its own process
// (the tailscale CLI keeps state between runs), and tags every line of
// output with the tailnet it came from.
func eachTailnet(cfgPath string, names, args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var failed []string
	for _, name := range names {
		cmd := exec.Command(exe, append([]string{"cli", "-config", cfgPath, "-profile", name, "--"}, args...)...)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			return err
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go prefixLines(&wg, os.Stdout, stdout, name)
		go prefixLines(&wg, os.Stderr, stderr, name)
		wg.Wait()
		if cmd.Wait() != nil {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed on %s", strings.Join(failed, ", "))
	}
	return nil
}

func prefixLines(wg *sync.WaitGroup, w io.Writer, r io.Reader, name string) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		fmt.Fprintf(w, "[%s] %s\n", name, sc.Text())
	}
	io.Copy(io.Discard, r) // a line too long for the buffer: don't block the child
}
