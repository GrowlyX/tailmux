package tun

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tailscale/wireguard-go/tun/tuntest"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// echoRequest builds an IPv4 ICMP echo request from src to dst.
func echoRequest(src, dst netip.Addr, id, seq uint16) []byte {
	const payload = "tailmux"
	pkt := make([]byte, header.IPv4MinimumSize+header.ICMPv4MinimumSize+len(payload))
	ip := header.IPv4(pkt)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(pkt)), TTL: 64, Protocol: uint8(header.ICMPv4ProtocolNumber),
		SrcAddr: tcpip.AddrFrom4(src.As4()), DstAddr: tcpip.AddrFrom4(dst.As4()),
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	icmp := header.ICMPv4(pkt[header.IPv4MinimumSize:])
	icmp.SetType(header.ICMPv4Echo)
	icmp.SetIdent(id)
	icmp.SetSequence(seq)
	copy(icmp.Payload(), payload)
	icmp.SetChecksum(^checksum(icmp))
	return pkt
}

func TestPing(t *testing.T) {
	if testing.Short() {
		t.Skip("spins up tailnets")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	m, cfg, _ := startMux(t, ctx)
	ct := tuntest.NewChannelTUN()
	fake := NewFakeIPs(netip.MustParsePrefix(cfg.TUN.Range), "")
	e, err := NewEngine(m, ct.TUN(), fake, 1500)
	if err != nil {
		t.Fatal(err)
	}
	go e.Run(ctx)

	me := netip.MustParseAddr("198.18.0.1")
	ping := func(dst netip.Addr, seq uint16, wait time.Duration) (header.IPv4, bool) {
		ct.Outbound <- echoRequest(me, dst, 7, seq)
		for {
			select {
			case b := <-ct.Inbound:
				ip := header.IPv4(b)
				icmp := header.ICMPv4(b[ip.HeaderLength():])
				if ip.Protocol() == uint8(header.ICMPv4ProtocolNumber) && icmp.Sequence() == seq {
					return ip, true
				}
			case <-time.After(wait):
				return nil, false
			}
		}
	}

	webBravo := fake.For("web.bravo")
	var reply header.IPv4
	var ok bool
	for seq := uint16(1); seq < 30 && !ok; seq++ { // the first ICMP ping can race the handshake
		reply, ok = ping(webBravo, seq, 5*time.Second)
	}
	if !ok {
		t.Fatal("no echo reply from web.bravo")
	}
	icmp := header.ICMPv4(reply[reply.HeaderLength():])
	if icmp.Type() != header.ICMPv4EchoReply || icmp.Ident() != 7 || string(icmp.Payload()) != "tailmux" {
		t.Errorf("bad reply: type %v ident %d payload %q", icmp.Type(), icmp.Ident(), icmp.Payload())
	}
	if got := netip.AddrFrom4(reply.SourceAddress().As4()); got != webBravo {
		t.Errorf("reply from %v, want %v", got, webBravo)
	}
	if !reply.IsChecksumValid() || checksum(icmp) != 0xffff {
		t.Error("bad checksums")
	}
	if _, ok := ping(netip.MustParseAddr("8.8.8.8"), 100, 3*time.Second); ok {
		t.Error("unclaimed destination should not answer")
	}
}
