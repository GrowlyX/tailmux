package tun

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// isEcho4 reports whether pkt is an IPv4 ICMP echo request.
func isEcho4(pkt []byte) bool {
	if len(pkt) < header.IPv4MinimumSize || pkt[0]>>4 != 4 {
		return false
	}
	ip := header.IPv4(pkt)
	if ip.Protocol() != uint8(header.ICMPv4ProtocolNumber) || ip.More() || ip.FragmentOffset() != 0 {
		return false
	}
	hl := int(ip.HeaderLength())
	if len(pkt) < hl+header.ICMPv4MinimumSize {
		return false
	}
	return header.ICMPv4(pkt[hl:]).Type() == header.ICMPv4Echo
}

// handlePing answers an echo request only if the destination really
// answers through its tailnet (or the exit node), after the real round
// trip, so `ping` shows true reachability and latency.
func (e *Engine) handlePing(pkt []byte) {
	ip := header.IPv4(pkt)
	dst := netip.AddrFrom4(ip.DestinationAddress().As4())

	host := dst.String()
	if e.fake.Prefix().Contains(dst) {
		name, ok := e.fake.Name(dst)
		if !ok {
			return
		}
		host = name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.m.Ping(ctx, host); err != nil {
		return
	}

	reply := echoReply(pkt)
	buf := make([]byte, offset+len(reply))
	copy(buf[offset:], reply)
	if _, err := e.dev.Write([][]byte{buf}, offset); err != nil && !isClosed(err) {
		return
	}
}

// echoReply turns an IPv4 echo request into its reply: swap addresses,
// flip the type, fix both checksums.
func echoReply(req []byte) []byte {
	pkt := append([]byte(nil), req...)
	ip := header.IPv4(pkt)
	src, dst := ip.SourceAddress(), ip.DestinationAddress()
	ip.SetSourceAddress(dst)
	ip.SetDestinationAddress(src)
	ip.SetTTL(64)
	ip.SetChecksum(0)
	ip.SetChecksum(^ip.CalculateChecksum())

	hl := int(ip.HeaderLength())
	icmp := header.ICMPv4(pkt[hl:int(ip.TotalLength())])
	icmp.SetType(header.ICMPv4EchoReply)
	icmp.SetChecksum(0)
	icmp.SetChecksum(^checksum(icmp))
	return pkt
}

func checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return uint16(sum)
}

func isClosed(err error) bool { return err == net.ErrClosed }
