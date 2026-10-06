#!/usr/bin/env python3
"""Serves a read-only fake of the daemon's API with tidy demo data: four
tailnets with smooth throughput and a Mullvad exit node. It's what
docs/panel-dark.png (and so the README banner) is rendered from.

usage: scripts/demo-daemon.py [port]      (default 21099)
then:  TAILMUX_API=http://127.0.0.1:21099 TailmuxBar --snapshot docs/panel-dark.png --dark
"""

import json
import math
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TAILNETS = [
    # name, suffix, online/peers, routes, peak bytes/s, phase
    ("work", "corp.ts.net", (14, 17), ["10.20.0.0/16 via gw", "10.30.0.0/16 via gw"], 1.5e6, 0.0),
    ("home", "tail1234.ts.net", (6, 6), ["192.168.50.0/24 via router"], 1.1e6, 1.7),
    ("lab", "lab.example.com", (9, 12), ["10.1.0.0/24 via r1", "10.2.0.0/24 via r2", "10.3.0.0/24 via r3"], 1.4e6, 3.1),
    ("staging", "tail9876.ts.net", (21, 26), [], 2.0e6, 4.4),
]

EXIT = {
    "tailnet": "home",
    "node": "se-sto-wg-001.mullvad.ts.net",
    "name": "se-sto-wg-001",
    "fqdn": "se-sto-wg-001.mullvad.ts.net",
    "online": True,
    "active": True,
    "location": {"country": "Sweden", "country_code": "SE", "city": "Stockholm", "city_code": "sto", "priority": 100},
}


def series(peak, phase, n=120):
    """A smooth, wavy throughput history, oldest first."""
    out = []
    for i in range(n):
        t = i / n * 2 * math.pi
        v = 0.5 + 0.42 * math.sin(1.5 * t + phase) + 0.06 * math.sin(11 * t + 2 * phase)
        out.append(max(0.0, v) * peak)
    return out


def status():
    return {
        "version": "1.0.0",
        "tun": {"interface": "utun7", "fake_range": "198.18.0.0/15", "dns": True, "exit_routes": True},
        "exit_node": EXIT,
        "tailnets": [
            {"name": n, "enabled": True, "state": "Running", "suffix": s, "self_ips": ["100.64.0.1"],
             "peers": p, "online": o, "routes": r}
            for n, s, (o, p), r, _, _ in TAILNETS
        ],
    }


def stats():
    return {"interval_ms": 1000, "tailnets": [
        {"name": n, "enabled": True, "state": "Running", "conns": 12, "rx_total": 1 << 30, "tx_total": 1 << 26,
         "rx": series(peak, ph), "tx": series(peak / 20, ph)}
        for n, _, _, _, peak, ph in TAILNETS
    ]}


def exit_nodes():
    node = {k: EXIT[k] for k in ("tailnet", "name", "fqdn", "online", "location")}
    return {"current": EXIT, "nodes": [dict(node, id="n1", ips=["100.90.0.1"], mullvad=True, selected=True)]}


ROUTES = {
    "/status": status,
    "/stats": stats,
    "/exit-nodes": exit_nodes,
    "/peers": lambda: [],
    "/logs": lambda: [],
    "/config": lambda: {"path": "/opt/homebrew/etc/tailmux/config.json", "state_dir": "/opt/homebrew/var/lib/tailmux", "config": {}},
}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        f = ROUTES.get(self.path.split("?")[0])
        if f is None:
            self.send_error(404)
            return
        body = json.dumps(f()).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 21099
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
