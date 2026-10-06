// Package tun is tailmux's proxy-free mode: the OS routes tailnet
// destinations into a TUN device, a userspace TCP/IP stack (gVisor)
// terminates each flow, and the flow is re-dialed through whichever
// tailnet owns it.
package tun

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	wgtun "github.com/tailscale/wireguard-go/tun"
	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/GrowlyX/tailmux/internal/mux"
)

// offset leaves room in front of each packet for the headers some TUN
// drivers want (4-byte AF header on macOS, virtio header on Linux).
const offset = 16

type Engine struct {
	m    *mux.Mux
	dev  wgtun.Device
	fake *FakeIPs
	// routed, if set, reports whether ip is routed into the TUN; tailnet
	// names whose real addresses are, and that no other tailnet claims,
	// are answered with those instead of a fake IP. Nil: always fake.
	routed func(ip netip.Addr) bool
	mtu    int

	s  *stack.Stack
	ep *channel.Endpoint
}

func NewEngine(m *mux.Mux, dev wgtun.Device, fake *FakeIPs, mtu int) (*Engine, error) {
	e := &Engine{m: m, dev: dev, fake: fake, mtu: mtu}
	e.s = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	sack := tcpip.TCPSACKEnabled(true)
	e.s.SetTransportProtocolOption(tcp.ProtocolNumber, &sack)

	e.ep = channel.New(1024, uint32(mtu), "")
	if err := e.s.CreateNIC(1, e.ep); err != nil {
		return nil, errors.New(err.String())
	}
	// Accept packets for any destination and answer from any source:
	// the stack impersonates every host behind the TUN.
	e.s.SetPromiscuousMode(1, true)
	e.s.SetSpoofing(1, true)
	e.s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: 1},
		{Destination: header.IPv6EmptySubnet, NIC: 1},
	})
	e.s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(e.s, 0, 4096, e.acceptTCP).HandlePacket)
	e.s.SetTransportProtocolHandler(udp.ProtocolNumber, udp.NewForwarder(e.s, e.acceptUDP).HandlePacket)
	return e, nil
}

// Run pumps packets between the device and the stack until ctx ends or
// the device fails. Transient errors (see transient) don't count as
// failing: closing the device would destroy the interface along with
// every route through it.
func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- e.fromDevice() }()
	go func() { errc <- e.toDevice(ctx) }()
	select {
	case <-ctx.Done():
		e.close()
		return nil
	case err := <-errc:
		e.close()
		return err
	}
}

func (e *Engine) close() {
	e.dev.Close()
	e.ep.Close()
	e.s.Close()
	e.fake.Save()
}

func (e *Engine) fromDevice() error {
	n := e.dev.BatchSize()
	// One slab for the whole batch; the device spaces packets out in it.
	slab := make([]byte, n*(65535+wgtun.ReadPacketSpacing)+wgtun.ReadPacketSpacing)
	packets := make([]wgtun.ReadPacket, n)
	var lim logLimiter
	var backoff time.Duration
	for {
		count, err := e.dev.Read(slab, packets)
		// Packets read before an error are still valid.
		for _, p := range packets[:count] {
			e.inject(slab[p.Offset : p.Offset+p.Size])
		}
		if err != nil {
			if errors.Is(err, wgtun.ErrTooManySegments) {
				continue
			}
			if !transient(err) {
				return err
			}
			// wireguard-go on macOS also reports its route-socket
			// listener's errors through Read, e.g. "route ip+net: no
			// buffer space available" while the network churns. The
			// device itself is fine; keep reading.
			lim.printf("tun read: %v (ignored)", err)
			backoff = min(max(2*backoff, 10*time.Millisecond), time.Second)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
	}
}

// inject hands one packet from the device to the stack (or answers a
// ping itself).
func (e *Engine) inject(pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	if isEcho4(pkt) {
		go e.handlePing(append([]byte(nil), pkt...))
		return
	}
	var proto tcpip.NetworkProtocolNumber
	switch pkt[0] >> 4 {
	case 4:
		proto = header.IPv4ProtocolNumber
	case 6:
		proto = header.IPv6ProtocolNumber
	default:
		return
	}
	pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(pkt)})
	e.ep.InjectInbound(proto, pb)
	pb.DecRef()
}

func (e *Engine) toDevice(ctx context.Context) error {
	var lim logLimiter
	for {
		pkt := e.ep.ReadContext(ctx)
		if pkt == nil {
			return ctx.Err()
		}
		v := pkt.ToView()
		pkt.DecRef()
		buf := make([]byte, offset+v.Size())
		copy(buf[offset:], v.AsSlice())
		v.Release()
		if _, err := e.dev.Write([][]byte{buf}, offset); err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				return err
			}
			// ENOBUFS and friends: the kernel queue is full. Drop the
			// packet like a congested link would; TCP retransmits.
			lim.printf("tun write: %v (packet dropped)", err)
		}
	}
}

// transient reports whether a device error is momentary resource
// pressure rather than the device going away.
func transient(err error) bool {
	for _, e := range []syscall.Errno{syscall.ENOBUFS, syscall.ENOMEM, syscall.EAGAIN, syscall.EINTR} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// logLimiter keeps a burst of identical errors from flooding the log.
type logLimiter struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (l *logLimiter) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.last) < 10*time.Second {
		l.suppressed++
		return
	}
	if l.suppressed > 0 {
		format += " (and %d more since the last report)"
		args = append(args, l.suppressed)
	}
	log.Printf(format, args...)
	l.last, l.suppressed = time.Now(), 0
}

func addrOf(a tcpip.Address) netip.Addr {
	ip, _ := netip.AddrFromSlice(a.AsSlice())
	return ip.Unmap()
}

// target turns a destination the OS sent us into something the mux can
// dial: fake addresses become the name they were handed out for.
func (e *Engine) target(dst netip.Addr, port uint16) (string, bool) {
	p := strconv.Itoa(int(port))
	if e.fake.Prefix().Contains(dst) {
		name, ok := e.fake.Name(dst)
		if !ok {
			return "", false
		}
		return net.JoinHostPort(name, p), true
	}
	return net.JoinHostPort(dst.String(), p), true
}

func (e *Engine) acceptTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	addr, ok := e.target(addrOf(id.LocalAddress), id.LocalPort)
	if !ok {
		r.Complete(true)
		return
	}
	// Dial first so a dead destination surfaces as a refused connection
	// rather than an accepted one that hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	up, tgt, err := e.m.DialTailnet(ctx, "tcp", addr)
	cancel()
	e.m.LogDial("tun", tgt, err)
	if err != nil {
		r.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	if terr != nil {
		r.Complete(true)
		up.Close()
		return
	}
	r.Complete(false)
	ep.SocketOptions().SetKeepAlive(true)
	c := gonet.NewTCPConn(&wq, ep)
	defer c.Close()
	defer up.Close()
	pipe(c, up)
}

func (e *Engine) acceptUDP(r *udp.ForwarderRequest) bool {
	id := r.ID()
	dst := addrOf(id.LocalAddress)
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	if terr != nil {
		return true
	}
	c := gonet.NewUDPConn(&wq, ep)
	if dst == e.fake.DNS() && id.LocalPort == 53 {
		go e.serveDNS(c)
		return true
	}
	addr, ok := e.target(dst, id.LocalPort)
	if !ok {
		c.Close()
		return true
	}
	go e.proxyUDP(c, addr)
	return true
}

const udpIdle = 2 * time.Minute

func (e *Engine) proxyUDP(c *gonet.UDPConn, addr string) {
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	up, tgt, err := e.m.DialTailnet(ctx, "udp", addr)
	cancel()
	e.m.LogDial("tun/udp", tgt, err)
	if err != nil {
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			src.SetReadDeadline(time.Now().Add(udpIdle))
			n, err := src.Read(buf)
			if err != nil {
				break
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go cp(up, c)
	go cp(c, up)
	<-done
}

func (e *Engine) serveDNS(c *gonet.UDPConn) {
	defer c.Close()
	buf := make([]byte, 1500)
	for {
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		if resp := e.answerDNS(buf[:n]); resp != nil {
			c.Write(resp)
		}
	}
}

// answerDNS answers tailnet names with their real addresses when those
// are unambiguous, else with a fake address (see addrsFor). Names that
// don't exist get NXDOMAIN, checked against the owning tailnet's own
// resolver. With an exit node, other names get their real addresses.
func (e *Engine) answerDNS(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true, RCode: dnsmessage.RCodeNameError}
	name := q.Name.String()
	var answers []netip.Addr
	ttl := uint32(60)
	if d := e.m.Router().RouteName(name); d.OK() {
		if len(d.IPs) > 0 {
			rh.RCode = dnsmessage.RCodeSuccess
			var ips []netip.Addr
			for _, s := range d.IPs {
				if ip, err := netip.ParseAddr(s); err == nil {
					ips = append(ips, ip)
				}
			}
			answers, ttl = e.addrsFor(name, d.Tailnet, ips)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			tgt, err := e.m.Resolve(ctx, name)
			cancel()
			switch {
			case err != nil && !mux.IsNotFound(err):
				// A lookup that failed rather than came back empty (say the
				// tailnet is reconnecting after sleep): SERVFAIL, which
				// resolvers retry, not NXDOMAIN, which they cache.
				rh.RCode = dnsmessage.RCodeServerFailure
			case err != nil:
				// NXDOMAIN
			case tgt.Tailnet == "":
				// Split DNS pointed outside the tailnet: hand back the real
				// addresses so the OS connects directly.
				rh.RCode = dnsmessage.RCodeSuccess
				answers = tgt.IPs
			default:
				rh.RCode = dnsmessage.RCodeSuccess
				answers, ttl = e.addrsFor(name, tgt.Tailnet, tgt.IPs)
			}
		}
	} else if e.m.ExitNode() != nil {
		// With an exit node the OS may send every name here ("~." on
		// Linux, "." on Windows): answer with real addresses, looked up
		// by the exit node's resolver.
		rh.Authoritative = false
		rh.RCode = dnsmessage.RCodeSuccess
		if q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeAAAA {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			tgt, err := e.m.Resolve(ctx, name)
			cancel()
			switch {
			case err != nil && mux.IsNotFound(err):
				rh.RCode = dnsmessage.RCodeNameError
			case err != nil:
				rh.RCode = dnsmessage.RCodeServerFailure
			default:
				answers = tgt.IPs
			}
		}
		// Other types get an empty answer: not NXDOMAIN, which resolvers
		// would apply to the name's A records too.
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	b.StartQuestions()
	b.Question(q)
	b.StartAnswers()
	rr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: ttl}
	for _, ip := range answers {
		switch {
		case q.Type == dnsmessage.TypeA && ip.Is4():
			b.AResource(rr, dnsmessage.AResource{A: ip.As4()})
		case q.Type == dnsmessage.TypeAAAA && ip.Is6() && !e.fake.Prefix().Contains(ip):
			b.AAAAResource(rr, dnsmessage.AAAAResource{AAAA: ip.As16()})
		}
	}
	out, _ := b.Finish()
	return out
}

// addrsFor picks the answer for a tailnet name: its real IPv4 addresses
// if each is routed into the TUN and reaches the same tailnet with no
// other tailnet claiming it, so `ping db.home` shows 100.x as it does with
// the official client. Otherwise (two tailnets numbering devices alike,
// or an address the TUN doesn't carry) a fake address unique to the
// name. Real answers get a short TTL: a collision can appear later.
func (e *Engine) addrsFor(name, tailnet string, ips []netip.Addr) ([]netip.Addr, uint32) {
	if e.routed != nil {
		r := e.m.Router()
		var addrs []netip.Addr
		for _, ip := range ips {
			if !ip.Is4() || !e.routed(ip) {
				continue
			}
			if d := r.RouteIP(ip); d.Tailnet != tailnet || len(d.Contested) > 0 {
				addrs = nil
				break
			}
			addrs = append(addrs, ip)
		}
		if len(addrs) > 0 {
			return addrs, 10
		}
	}
	return []netip.Addr{e.fake.For(name)}, 60
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}
