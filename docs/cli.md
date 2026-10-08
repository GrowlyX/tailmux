# The tailscale CLI, per tailnet

tailmux has the `tailscale` CLI built in. `tailmux cli` runs any tailscale
command against one of your tailnets, or each in turn:

```sh
tailmux cli -profile work status
tailmux cli -profile work configure kubeconfig my-cluster
tailmux cli -profile home cert nas.home-tailnet.ts.net
tailmux cli -profile '*' ip -4          # quote the *: your shell expands it otherwise
```

```
[home] 100.101.102.103
[work] 100.64.0.7
```

With one tailnet, `-profile` can be left out. With `'*'`, every line of
output is tagged with its tailnet, and the command fails if it failed on
any of them. Like a shell loop, the runs share your input: piped input
goes to whichever run reads it first.

The CLI is the version tailmux's own nodes run, so the two always agree.
It needs `tailmux up` running.

## What works

Commands that talk to the node, such as `status`, `ip`, `ping`, `whois`,
`nc`, `dns status`, `cert`, `serve`, `lock sign` and `configure kubeconfig`.

Not useful here:

- `ssh`: plain `ssh host` already reaches every tailnet through tailmux.
  `tailmux cli` refuses it, because `tailscale ssh` replaces itself with
  `ssh` and takes its connection to tailmux with it.
- `up`, `down`, `login`, `logout`, `set`: they work, but tailmux manages
  these. Use `tailmux setup`, the menu bar app, or `tailmux exit-node`;
  tailmux turns accepting subnet routes back on when it starts.
- `switch`: each tailnet is one node with one login.
- `update`, `web`, and `configure` subcommands for systems tailscale runs on
  (Synology, systray, ...).

## How it reaches the node

Each tailnet's LocalAPI, the API the tailscale CLI talks to, is served
through tailmux's HTTP API at `/tailnets/<name>/localapi/`, and only to
requests that carry an `X-Tailmux` header, so web pages can't reach it.
`tailmux cli` opens a socket that only you can use (a named pipe on
Windows), forwards it there, and runs the CLI with `--socket` pointing at it.

Like the rest of the local API, it's open to every user on the machine.
On a shared machine, anyone can run tailscale commands as your nodes.
