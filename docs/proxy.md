# Proxy mode

Always on, with or without [TUN mode](tun.md).

| Client | Setting |
| --- | --- |
| ssh | `ProxyCommand tailmux nc %h %p` in `~/.ssh/config` |
| curl, most CLIs | `ALL_PROXY=socks5h://127.0.0.1:1055` (the `h` makes tailmux resolve names) |
| kubectl | `proxy-url: socks5://127.0.0.1:1055` on the cluster in your kubeconfig |
| Browsers, all of macOS | Automatic proxy configuration: `http://127.0.0.1:1056/proxy.pac` |
| Anything HTTP | `HTTPS_PROXY=http://127.0.0.1:1056` |

The PAC file is generated from the live routing table. Tailnet destinations go
through tailmux; everything else stays `DIRECT`.

### An extra DNS server (optional)

Add `"dns": "127.0.0.1:1053"` at the top level of your config file (next to
`"socks5"` and `"http"`; `tailmux example-config` shows the layout) and
restart tailmux. tailmux then also runs a DNS server on that address, which
answers tailnet names with their real addresses:

```sh
dig @127.0.0.1 -p 1053 +short db.home
```

Nothing uses it on its own. It's for pointing tools or your own resolver
setup at, for example a macOS `/etc/resolver/home` file with `nameserver
127.0.0.1` and `port 1053`. **You don't need it in [TUN mode](tun.md)**,
which configures the system resolver itself. It doesn't change what TUN mode
answers either (see [why 198.18.x.x](tun.md#why-does-a-name-resolve-to-19818xx)).
