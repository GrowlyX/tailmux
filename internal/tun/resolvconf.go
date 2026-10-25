package tun

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// resolvConf points /etc/resolv.conf at tailmux, for Linux systems without
// systemd-resolved ("tun": {"resolv_conf": true}). The file has no way to
// send only some domains somewhere, so every lookup comes to tailmux, which
// answers tailnet names and forwards the rest to the nameservers the
// original file listed. The original is moved aside (a symlink stays a
// symlink) and put back when tailmux stops, or at the next start after a
// crash.
type resolvConf struct {
	path, backup string

	mu        sync.Mutex
	server    netip.Addr
	upstreams []netip.AddrPort
}

const resolvConfMarker = "# managed by tailmux"

func newResolvConf(path string) *resolvConf {
	return &resolvConf{path: path, backup: filepath.Join(filepath.Dir(path), "resolv.pre-tailmux-backup.conf")}
}

func ownResolvConf(b []byte) bool { return bytes.HasPrefix(b, []byte(resolvConfMarker)) }

// set makes server the only nameserver, keeping the original's search
// domains and options.
func (r *resolvConf) set(server netip.Addr) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, readErr := os.ReadFile(r.path)
	if _, err := os.Lstat(r.path); err == nil && !ownResolvConf(cur) {
		// The system's file, or one written over ours since: that is the
		// original now. One we can't read (a symlink to a file that isn't
		// there yet) stays: without its nameservers, every other name
		// would fail.
		if readErr != nil {
			return fmt.Errorf("read %s: %w", r.path, readErr)
		}
		err := os.Rename(r.path, r.backup)
		if errors.Is(err, syscall.EBUSY) {
			err = writeFileAtomic(r.backup, cur) // a bind mount (containers)
		}
		if err != nil {
			return fmt.Errorf("back up %s: %w", r.path, err)
		}
	}
	var orig []byte
	if _, err := os.Lstat(r.backup); err == nil {
		// Read each time: through a symlink, it stays current.
		if orig, err = os.ReadFile(r.backup); err != nil {
			return errors.Join(fmt.Errorf("read %s: %w", r.backup, err), r.putBackLocked())
		}
	}
	upstreams, keep := parseResolvConf(orig, server)
	var b strings.Builder
	fmt.Fprintf(&b, "%s; the original is %s and comes back when tailmux stops.\n", resolvConfMarker, r.backup)
	fmt.Fprintf(&b, "# Tailnet names are answered by tailmux, the rest by the nameservers listed there.\n")
	fmt.Fprintf(&b, "nameserver %s\n", server)
	for _, l := range keep {
		b.WriteString(l + "\n")
	}
	if err := writeResolvConf(r.path, []byte(b.String())); err != nil {
		// Never leave the system without a resolver.
		return errors.Join(err, r.putBackLocked())
	}
	r.server, r.upstreams = server, upstreams
	return nil
}

// intact reports whether the file is still the one set wrote.
func (r *resolvConf) intact() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	return err == nil && ownResolvConf(b) && bytes.Contains(b, []byte("\nnameserver "+r.server.String()+"\n"))
}

// dnsUpstreams are the original nameservers, while the file points at
// tailmux; nil otherwise.
func (r *resolvConf) dnsUpstreams() []netip.AddrPort {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upstreams
}

// restore puts the original back. If something else replaced our file in
// the meantime, that file stays and the stale backup goes.
func (r *resolvConf) restore() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restoreLocked()
}

func (r *resolvConf) restoreLocked() error {
	r.upstreams = nil
	cur, readErr := os.ReadFile(r.path)
	_, curErr := os.Lstat(r.path)
	if curErr == nil && readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", r.path, readErr) // can't tell whose it is
	}
	_, bakErr := os.Lstat(r.backup)
	switch {
	case curErr == nil && !ownResolvConf(cur):
		if bakErr == nil {
			return os.Remove(r.backup)
		}
		return nil
	case bakErr == nil:
		return r.moveBackLocked()
	case curErr == nil:
		// Ours, and there was no original.
		return os.Remove(r.path)
	}
	return nil
}

// putBackLocked undoes a takeover that failed partway. Whatever the file
// holds now (possibly nothing, after a failed in-place write) is ours.
func (r *resolvConf) putBackLocked() error {
	r.upstreams = nil
	if _, err := os.Lstat(r.backup); err == nil {
		return r.moveBackLocked()
	}
	if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (r *resolvConf) moveBackLocked() error {
	err := os.Rename(r.backup, r.path)
	if errors.Is(err, syscall.EBUSY) {
		b, rerr := os.ReadFile(r.backup)
		if err = errors.Join(rerr, replaceFile(r.path, b)); err == nil {
			err = os.Remove(r.backup)
		}
	}
	return err
}

// parseResolvConf returns the nameservers in a resolv.conf other than
// tailmux's own, and the search, domain and options lines to carry over.
func parseResolvConf(b []byte, self netip.Addr) (upstreams []netip.AddrPort, keep []string) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "nameserver":
			if ip, err := netip.ParseAddr(f[1]); err == nil && ip != self && !ip.IsUnspecified() {
				upstreams = append(upstreams, netip.AddrPortFrom(ip, 53))
			}
		case "search", "domain", "options":
			keep = append(keep, line)
		}
	}
	return upstreams, keep
}

// writeResolvConf is replaceFile, swapped out by tests.
var writeResolvConf = replaceFile

// replaceFile writes path atomically or, where it is a bind mount that
// can't be renamed over (containers), in place.
func replaceFile(path string, data []byte) error {
	err := writeFileAtomic(path, data)
	if errors.Is(err, syscall.EBUSY) {
		err = os.WriteFile(path, data, 0o644)
	}
	return err
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tailmux-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Chmod(0o644), f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
