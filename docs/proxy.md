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

With `"dns": "127.0.0.1:1053"` tailmux also runs a DNS server that answers
tailnet names with their real addresses.
