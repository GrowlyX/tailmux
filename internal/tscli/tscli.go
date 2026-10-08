// Package tscli runs the tailscale CLI against one of tailmux's tailnets.
//
// Each tailnet's LocalAPI is reachable through tailmux's HTTP API at
// /tailnets/{name}/localapi/. The tailscale CLI only speaks to a socket,
// so Bridge listens on one only the current user can reach (a Unix
// socket, or a named pipe on Windows) and forwards every request to it.
package tscli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"tailscale.com/cmd/tailscale/cli"
	"tailscale.com/safesocket"
)

// Bridge is a socket that stands in for one tailnet's tailscaled.
type Bridge struct {
	Path string // pass to the tailscale CLI as --socket
	srv  *http.Server
	dir  string
}

// NewBridge serves tailnet's LocalAPI, reached through the tailmux HTTP
// API at api (host:port), on a fresh socket.
func NewBridge(api, tailnet string) (*Bridge, error) {
	b := &Bridge{}
	if runtime.GOOS == "windows" {
		var r [8]byte
		rand.Read(r[:])
		b.Path = `\\.\pipe\tailmux-cli-` + hex.EncodeToString(r[:])
	} else {
		dir, err := os.MkdirTemp("", "tailmux-cli-")
		if err != nil {
			return nil, err
		}
		b.dir = dir
		b.Path = filepath.Join(dir, "tailscaled.sock")
	}
	ln, err := safesocket.ListenCurrentUser(b.Path)
	if err != nil {
		b.Close()
		return nil, err
	}
	base := &url.URL{Scheme: "http", Host: api, Path: "/tailnets/" + tailnet + "/"}
	b.srv = &http.Server{Handler: &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// /localapi/v0/status -> /tailnets/{name}/localapi/v0/status
			pr.SetURL(base)
			pr.Out.Host = api
			pr.Out.Header.Set("X-Tailmux", "1")
		},
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		FlushInterval: -1,
	}}
	go b.srv.Serve(ln)
	return b, nil
}

func (b *Bridge) Close() error {
	var err error
	if b.srv != nil {
		err = b.srv.Close()
	}
	if b.dir != "" {
		err = errors.Join(err, os.RemoveAll(b.dir))
	}
	return err
}

// Run runs the tailscale CLI with args against tailnet.
func Run(ctx context.Context, api, tailnet string, args []string) error {
	b, err := NewBridge(api, tailnet)
	if err != nil {
		return err
	}
	defer b.Close()
	return cli.RunWithContext(ctx, append([]string{"--socket", b.Path}, args...))
}
