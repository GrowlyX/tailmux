package mux

import (
	"context"
	"log"
	"net"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// ServeDNS answers A/AAAA queries for every name some tailnet claims
// (MagicDNS names, host.<tailnet-name> aliases, split-DNS domains) and
// NXDOMAIN for the rest. Point per-domain resolvers at it, e.g. macOS
// /etc/resolver/<suffix> files.
func (m *Mux) ServeDNS(pc net.PacketConn) error {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		q := append([]byte(nil), buf[:n]...)
		go func() {
			if resp := m.answerDNS(q); resp != nil {
				pc.WriteTo(resp, addr)
			}
		}()
	}
}

func (m *Mux) answerDNS(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RCode: dnsmessage.RCodeNameError}

	var tgt Target
	name := q.Name.String()
	if d := m.Router().RouteName(name); d.OK() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		tgt, err = m.Resolve(ctx, name)
		cancel()
		if err == nil {
			rh.RCode = dnsmessage.RCodeSuccess
		} else {
			log.Printf("dns %s: %v", name, err)
		}
	}

	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	b.StartQuestions()
	b.Question(q)
	b.StartAnswers()
	rr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 30}
	for _, ip := range tgt.IPs {
		switch {
		case q.Type == dnsmessage.TypeA && ip.Is4():
			b.AResource(rr, dnsmessage.AResource{A: ip.As4()})
		case q.Type == dnsmessage.TypeAAAA && ip.Is6():
			b.AAAAResource(rr, dnsmessage.AAAAResource{AAAA: ip.As16()})
		}
	}
	out, _ := b.Finish()
	return out
}
