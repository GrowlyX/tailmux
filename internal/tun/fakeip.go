package tun

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FakeIPs hands out a unique address per tailnet name. Every tailnet
// numbers its devices from the same 100.64.0.0/10, so real addresses
// collide; a fake one maps back to the name, and the name routes
// unambiguously.
//
// Layout inside the range: .0 network, .1 the interface, .53 DNS, and
// names from base+256 upward.
type FakeIPs struct {
	mu     sync.Mutex
	pfx    netip.Prefix
	next   netip.Addr
	byName map[string]netip.Addr
	byIP   map[netip.Addr]string
	path   string
	dirty  bool
}

func NewFakeIPs(pfx netip.Prefix, path string) *FakeIPs {
	pfx = pfx.Masked()
	f := &FakeIPs{pfx: pfx, byName: map[string]netip.Addr{}, byIP: map[netip.Addr]string{}, path: path}
	f.next = f.first()
	f.load()
	return f
}

func (f *FakeIPs) Prefix() netip.Prefix { return f.pfx }

// Gateway is the interface's own address.
func (f *FakeIPs) Gateway() netip.Addr { return f.pfx.Addr().Next() }

// DNS is where the OS sends queries for tailnet domains.
func (f *FakeIPs) DNS() netip.Addr { return nth(f.pfx.Addr(), 53) }

func (f *FakeIPs) first() netip.Addr { return nth(f.pfx.Addr(), 256) }

func nth(a netip.Addr, n int) netip.Addr {
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v += uint32(n)
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func normName(n string) string { return strings.ToLower(strings.TrimSuffix(n, ".")) }

// For returns name's fake address, allocating one if needed. When the
// range is used up it wraps and reuses the oldest addresses.
func (f *FakeIPs) For(name string) netip.Addr {
	name = normName(name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if ip, ok := f.byName[name]; ok {
		return ip
	}
	ip := f.next
	f.next = ip.Next()
	if !f.pfx.Contains(f.next) {
		f.next = f.first()
	}
	if old, ok := f.byIP[ip]; ok {
		delete(f.byName, old)
	}
	f.byName[name] = ip
	f.byIP[ip] = name
	f.dirty = true
	return ip
}

// Name maps a fake address back to its name.
func (f *FakeIPs) Name(ip netip.Addr) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.byIP[ip]
	return n, ok
}

// The mapping is saved so that addresses the OS already cached keep
// working across restarts.
type fakeState struct {
	Prefix string            `json:"prefix"`
	Next   string            `json:"next"`
	Names  map[string]string `json:"names"`
}

func (f *FakeIPs) load() {
	if f.path == "" {
		return
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	var st fakeState
	if json.Unmarshal(b, &st) != nil || st.Prefix != f.pfx.String() {
		return
	}
	for name, s := range st.Names {
		if ip, err := netip.ParseAddr(s); err == nil && f.pfx.Contains(ip) {
			f.byName[name] = ip
			f.byIP[ip] = name
		}
	}
	if ip, err := netip.ParseAddr(st.Next); err == nil && f.pfx.Contains(ip) && ip.Compare(f.first()) >= 0 {
		f.next = ip
	}
}

func (f *FakeIPs) Save() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path == "" || !f.dirty {
		return nil
	}
	st := fakeState{Prefix: f.pfx.String(), Next: f.next.String(), Names: map[string]string{}}
	for n, ip := range f.byName {
		st.Names[n] = ip.String()
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	os.MkdirAll(filepath.Dir(f.path), 0o700)
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	f.dirty = false
	return os.Rename(tmp, f.path)
}
