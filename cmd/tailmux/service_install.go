//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/GrowlyX/tailmux/internal/mux"
)

// seedSystemConfig makes sure the service has a config: the invoking
// user's if they have one (so tailnets set up in `tailmux setup` carry
// over), else a starter with TUN mode on.
func seedSystemConfig(path string, userConfig string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if userConfig != "" {
		if c, err := mux.ReadConfig(userConfig); err == nil && len(c.Tailnets) > 0 {
			c.StateDir = "" // the service keeps its own
			c.TUN.Enabled = true
			fmt.Printf("using the tailnets from %s\n", userConfig)
			return c.Save(path)
		}
	}
	var c mux.Config
	if err := json.Unmarshal([]byte(exampleConfig), &c); err != nil {
		return err
	}
	c.TUN.Enabled = true
	fmt.Printf("wrote a starter config to %s; add your tailnets with `tailmux setup -config %s`\n", path, path)
	return c.Save(path)
}

// installBinary copies the running executable to dst (unless it already
// runs from there), so the service doesn't depend on wherever it was
// launched from, like an AppImage mount that disappears.
func installBinary(dst string) (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil {
		return "", err
	}
	if abs, _ := filepath.Abs(dst); abs == src {
		return src, nil
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		return "", fmt.Errorf("install %s: %w", dst, err)
	}
	return dst, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, dst)
}
