package mux

import (
	"net"
	"net/netip"
	"testing"
)

// A service bound to one LAN address is found whichever address it is,
// not only when it's on the first interface.
func TestDialLANFindsEachAddress(t *testing.T) {
	ips, err := lanAddrs()
	if err != nil {
		t.Skip(err)
	}
	for _, ip := range ips {
		ln, err := net.Listen("tcp", netip.AddrPortFrom(ip, 0).String())
		if err != nil {
			t.Fatal(err)
		}
		port := uint16(ln.Addr().(*net.TCPAddr).Port)
		go func() {
			if c, err := ln.Accept(); err == nil {
				c.Close()
			}
		}()
		c, err := dialLAN(port)
		ln.Close()
		if err != nil {
			t.Errorf("service on %v: %v", ip, err)
			continue
		}
		c.Close()
	}
}

func TestDialLANSkipsLoopback(t *testing.T) {
	if _, err := lanAddrs(); err != nil {
		t.Skip(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if c, err := dialLAN(uint16(ln.Addr().(*net.TCPAddr).Port)); err == nil {
		c.Close()
		t.Error("reached a 127.0.0.1-only service")
	}
}
