package tun

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	wgtun "github.com/tailscale/wireguard-go/tun"
	"github.com/tailscale/wireguard-go/tun/tuntest"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// faultyTUN returns queued errors from Read and Write before passing
// through to the real device.
type faultyTUN struct {
	wgtun.Device
	mu        sync.Mutex
	readErrs  []error
	writeErrs []error
}

func (f *faultyTUN) pop(q *[]error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(*q) == 0 {
		return nil
	}
	err := (*q)[0]
	*q = (*q)[1:]
	return err
}

func (f *faultyTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if err := f.pop(&f.readErrs); err != nil {
		return 0, err
	}
	return f.Device.Read(bufs, sizes, offset)
}

func (f *faultyTUN) Write(bufs [][]byte, offset int) (int, error) {
	if err := f.pop(&f.writeErrs); err != nil {
		return 0, err
	}
	return f.Device.Write(bufs, offset)
}

// What wireguard-go's macOS route listener pushes through Read when
// net.InterfaceByIndex hits ENOBUFS.
var routeENOBUFS = &net.OpError{Op: "route", Net: "ip+net", Err: os.NewSyscallError("sysctl", syscall.ENOBUFS)}

func TestTransient(t *testing.T) {
	for _, err := range []error{routeENOBUFS, syscall.ENOBUFS, &os.PathError{Op: "write", Path: "utun5", Err: syscall.ENOBUFS}, syscall.ENOMEM} {
		if !transient(err) {
			t.Errorf("transient(%v) = false", err)
		}
	}
	for _, err := range []error{os.ErrClosed, net.ErrClosed, syscall.ENXIO, errors.New("device gone")} {
		if transient(err) {
			t.Errorf("transient(%v) = true", err)
		}
	}
}

// TestEngineSurvivesTransientErrors: ENOBUFS from the device must not
// stop the engine. Before the fix, Run returned and closed the device,
// which on macOS destroys the utun.
func TestEngineSurvivesTransientErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ct := tuntest.NewChannelTUN()
	dev := &faultyTUN{Device: ct.TUN(),
		readErrs:  []error{routeENOBUFS, routeENOBUFS, syscall.ENOBUFS},
		writeErrs: []error{syscall.ENOBUFS, syscall.ENOBUFS},
	}
	fake := NewFakeIPs(netip.MustParsePrefix("198.18.0.0/15"), "")
	e, err := NewEngine(nil, dev, fake, 1500)
	if err != nil {
		t.Fatal(err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- e.Run(ctx) }()

	// A fake address with no name behind it: the engine answers a SYN
	// with RST without involving the mux, which proves packets flow both
	// ways. The first RSTs are dropped by the failing writes; the
	// client's SYN retransmits get through.
	host := osStack(t, ctx, ct)
	dst := netip.MustParseAddr("198.18.9.9")
	dctx, dcancel := context.WithTimeout(ctx, 20*time.Second)
	defer dcancel()
	_, err = gonet.DialContextTCP(dctx, host, full(dst, 80), ipv4.ProtocolNumber)
	if err == nil || !errors.Is(err, syscall.ECONNREFUSED) && !strings.Contains(err.Error(), "refused") {
		t.Fatalf("dial through the engine: got %v, want connection refused", err)
	}
	select {
	case err := <-runErr:
		t.Fatalf("engine stopped on a transient error: %v", err)
	default:
	}
	if len(dev.readErrs)+len(dev.writeErrs) != 0 {
		t.Fatal("not all injected errors were hit")
	}
}

// TestEngineStopsOnFatalError: a real device failure still stops Run, so
// System can replace the device.
func TestEngineStopsOnFatalError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gone := errors.New("device gone")
	dev := &faultyTUN{Device: tuntest.NewChannelTUN().TUN(), readErrs: []error{gone}}
	e, err := NewEngine(nil, dev, NewFakeIPs(netip.MustParsePrefix("198.18.0.0/15"), ""), 1500)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(ctx); !errors.Is(err, gone) {
		t.Fatalf("Run = %v, want %v", err, gone)
	}
}
