package mux

import (
	"net/http"
	"net/http/httputil"

	"tailscale.com/client/tailscale/apitype"
)

// serveLocalAPI passes /tailnets/{name}/localapi/... through to that
// tailnet's own LocalAPI, the API the tailscale CLI talks to, so
// `tailmux cli` can run any tailscale command against any tailnet.
// Streams (watch-ipn-bus) and upgrades (the dial behind `tailscale nc`)
// pass through too.
func (m *Mux) serveLocalAPI(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	t := m.Tailnet(r.PathValue("name"))
	if t == nil {
		http.Error(w, "no tailnet "+r.PathValue("name"), http.StatusNotFound)
		return
	}
	t.startMu.Lock()
	lc := t.lc
	t.startMu.Unlock()
	if lc == nil {
		http.Error(w, t.cfg.Name+" hasn't started", http.StatusServiceUnavailable)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = apitype.LocalAPIHost
			pr.Out.URL.Path = "/localapi/" + r.PathValue("path")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = apitype.LocalAPIHost
			pr.Out.Header.Del("X-Tailmux")
			if pr.Out.Header.Get("Upgrade") != "" {
				// ReverseProxy sends "Upgrade"; LocalAPI only takes "upgrade".
				pr.Out.Header.Set("Connection", "upgrade")
			}
		},
		Transport:     &http.Transport{DialContext: lc.Dial, DisableKeepAlives: true},
		FlushInterval: -1,
	}
	rp.ServeHTTP(w, r)
}
