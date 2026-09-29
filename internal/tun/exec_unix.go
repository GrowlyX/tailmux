//go:build darwin || linux

package tun

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }
