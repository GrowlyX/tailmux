package mux

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// ServeSOCKS5 runs a no-auth SOCKS5 server (CONNECT only). Clients that
// send hostnames (socks5h) get tailnet-aware name resolution.
func (m *Mux) ServeSOCKS5(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go m.handleSOCKS(c)
	}
}

const (
	socksOK            = 0
	socksFail          = 1
	socksNotAllowed    = 2
	socksNetUnreach    = 3
	socksHostUnreach   = 4
	socksRefused       = 5
	socksCmdNotSupport = 7
)

func (m *Mux) handleSOCKS(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)

	// Greeting: VER NMETHODS METHODS...
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := br.Discard(int(hdr[1])); err != nil {
		return
	}
	c.Write([]byte{5, 0}) // no auth

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	var req [4]byte
	if _, err := io.ReadFull(br, req[:]); err != nil || req[0] != 5 {
		return
	}
	host, err := readSOCKSAddr(br, req[3])
	if err != nil {
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(br, pb[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb[:])
	if req[1] != 1 { // CONNECT
		socksReply(c, socksCmdNotSupport, nil)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	up, tgt, err := m.Dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	cancel()
	m.LogDial("socks", tgt, err)
	if err != nil {
		socksReply(c, socksCode(err), nil)
		return
	}
	defer up.Close()
	socksReply(c, socksOK, up.LocalAddr())
	c.SetDeadline(time.Time{})
	// Anything the client pipelined after the request is in br.
	pipe(c, br, up)
}

func readSOCKSAddr(r *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return netip.AddrFrom4(b).String(), nil
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return netip.AddrFrom16(b).Unmap().String(), nil
	case 3:
		n, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", fmt.Errorf("socks: bad address type %d", atyp)
}

func socksReply(c net.Conn, code byte, bound net.Addr) {
	resp := []byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}
	if ta, ok := bound.(*net.TCPAddr); ok {
		if ap := ta.AddrPort(); ap.Addr().Unmap().Is4() {
			a := ap.Addr().Unmap().As4()
			copy(resp[4:8], a[:])
			binary.BigEndian.PutUint16(resp[8:], ap.Port())
		}
	}
	c.Write(resp)
}

func socksCode(err error) byte {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, ErrDirectDisabled):
		return socksNotAllowed
	case errors.As(err, &dnsErr):
		return socksHostUnreach
	case errors.Is(err, context.DeadlineExceeded):
		return socksHostUnreach
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return socksRefused
	}
	return socksFail
}

// pipe copies both ways until either side is done, half-closing where
// the connection supports it.
func pipe(client net.Conn, clientR io.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(up, clientR)
		closeWrite(up)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, up)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
