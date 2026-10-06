package tun

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	wgtun "github.com/tailscale/wireguard-go/tun"
	"github.com/tailscale/wireguard-go/tun/tuntest"

	"github.com/GrowlyX/tailmux/internal/mux"
)

type recordingOS struct {
	calls   []string
	flushes int
	lostDNS bool
	onUp    func()
	onClose func()
}

func (o *recordingOS) up(string, netip.Addr, netip.Prefix, int) error {
	o.calls = append(o.calls, "up")
	if o.onUp != nil {
		o.onUp()
	}
	return nil
}
func (o *recordingOS) addRoute(string, netip.Prefix) error {
	o.calls = append(o.calls, "add route")
	return nil
}
func (o *recordingOS) delRoute(string, netip.Prefix) error {
	o.calls = append(o.calls, "delete route")
	return nil
}
func (o *recordingOS) setDNS(string, []string, []string, netip.Addr) (bool, error) {
	o.calls = append(o.calls, "DNS")
	o.lostDNS = false
	return true, nil
}
func (o *recordingOS) dnsIntact(string, []string, []string) bool { return !o.lostDNS }
func (o *recordingOS) setExit(string, bool) error                { return nil }
func (o *recordingOS) flushDNS()                                 { o.flushes++ }
func (o *recordingOS) close(string) error {
	o.calls = append(o.calls, "close")
	if o.onClose != nil {
		o.onClose()
	}
	return nil
}

type countedDevice struct {
	wgtun.Device
	closes int
}

func (d *countedDevice) Close() error {
	d.closes++
	if d.Device != nil {
		return d.Device.Close()
	}
	return nil
}

func lifecycleSystem(t *testing.T, o *recordingOS) *System {
	t.Helper()
	// A real, existing name models another client reusing the closed TUN's
	// name. OS writes are recorded; no real interfaces or routes are changed.
	ifs, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var name string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 {
			name = ifc.Name
			break
		}
	}
	if name == "" {
		t.Fatal("no loopback interface")
	}
	cfg := &mux.Config{StateDir: t.TempDir(), Tailnets: []mux.TailnetConfig{{Name: "alpha"}}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	return &System{m: mux.New(cfg, mux.Options{}), cfg: cfg, os: o,
		fake: NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), ""), ifname: name,
		routes: map[netip.Prefix]bool{}, skipped: map[netip.Prefix]bool{}}
}

func TestRetiredDeviceCannotRepairReusedInterface(t *testing.T) {
	o := &recordingOS{}
	s := lifecycleSystem(t, o)
	gone := errors.New("device failed")
	raw := &countedDevice{Device: &faultyTUN{
		Device: tuntest.NewChannelTUN().TUN(), readErrs: []error{gone},
	}}
	dev := &systemDevice{Device: raw, system: s}
	s.dev, s.dnsActive = dev, true
	o.onClose = func() {
		if raw.closes != 0 {
			t.Error("OS cleanup ran after releasing the interface")
		}
	}
	eng, err := NewEngine(nil, dev, s.fake, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Run(t.Context()); !errors.Is(err, gone) {
		t.Fatalf("Run = %v, want %v", err, gone)
	}
	if s.dev != nil || s.DNSActive() || raw.closes != 1 {
		t.Fatal("failed device was not retired")
	}
	s.Repair("reused interface name")
	s.reconcile(true) // tailnet updates also configure routes
	s.check()         // and the periodic watcher does too
	s.cancel = func() {}
	s.done = make(chan struct{})
	close(s.done)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if len(o.calls) != 1 || o.calls[0] != "close" {
		t.Fatalf("configured a retired interface: %v", o.calls)
	}
}

func TestDeviceCloseWaitsForRepair(t *testing.T) {
	o := &recordingOS{}
	s := lifecycleSystem(t, o)
	raw := &countedDevice{}
	dev := &systemDevice{Device: raw, system: s}
	s.dev = dev
	entered, release := make(chan struct{}), make(chan struct{})
	repaired, closing, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	o.onUp = func() { close(entered); <-release }
	go func() { s.Repair(""); close(repaired) }()
	<-entered
	go func() { close(closing); dev.Close(); close(closed) }()
	<-closing
	select {
	case <-closed:
		t.Error("released the interface during OS configuration")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan struct{}{repaired, closed} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("repair and device closure did not finish")
		}
	}
	if raw.closes != 1 || o.calls[len(o.calls)-1] != "close" {
		t.Fatalf("device did not close after configuration: %v", o.calls)
	}

	// A delayed close from the old engine must not close a replacement
	// or remove its DNS configuration, even with the same interface name.
	next := &countedDevice{}
	s.dev = &systemDevice{Device: next, system: s}
	o.calls, o.onUp = nil, nil
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if len(o.calls) != 0 || next.closes != 0 || s.dev == nil {
		t.Fatal("old device closed its replacement")
	}
	s.Repair("")
	if len(o.calls) == 0 || o.calls[0] != "up" {
		t.Fatal("replacement could not be configured")
	}
	s.dev.Close()
}

func TestReconcileSearchDomains(t *testing.T) {
	o := &recordingOS{}
	s := lifecycleSystem(t, o)
	s.dev = &systemDevice{Device: &countedDevice{}, system: s}
	// No started tailnets: the old search list must be removed even though
	// the sorted match domains have not changed.
	s.searchDomains = []string{"alpha"}
	s.cfg.TUN.NoDNS = true
	s.reconcile(false)
	if len(o.calls) != 0 {
		t.Fatalf("no_dns configured DNS: %v", o.calls)
	}
	s.cfg.TUN.NoDNS = false
	s.reconcile(false)
	if len(o.calls) != 1 || o.calls[0] != "DNS" || len(s.searchDomains) != 0 || o.flushes != 1 {
		t.Fatalf("search list not updated: calls=%v search=%v flushes=%d", o.calls, s.searchDomains, o.flushes)
	}
	s.reconcile(false)
	if len(o.calls) != 1 || o.flushes != 1 {
		t.Fatal("unchanged DNS configuration rewritten")
	}
	o.lostDNS = true
	s.check()
	if len(o.calls) != 3 || o.calls[1] != "up" || o.calls[2] != "DNS" || o.flushes != 2 {
		t.Fatalf("lost DNS not repaired and flushed: calls=%v flushes=%d", o.calls, o.flushes)
	}
	s.dev.Close()
}
