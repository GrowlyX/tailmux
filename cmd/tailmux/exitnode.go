package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/GrowlyX/tailmux/internal/mux"
)

type exitNodes struct {
	Current *mux.ExitStatus    `json:"current"`
	Nodes   []mux.ExitNodeInfo `json:"nodes"`
}

// exitNode is `tailmux exit-node [list [filter] | use <node> | off]`.
func exitNode(cfg *mux.Config, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	var en exitNodes
	if err := apiGet(cfg, "/exit-nodes", &en); err != nil {
		return err
	}
	switch sub {
	case "list", "ls":
		return listExitNodes(en, strings.Join(args, " "))
	case "use", "set":
		if len(args) == 0 {
			return fmt.Errorf("usage: tailmux exit-node use <name|name.tailnet|ip|country|city>")
		}
		n, err := pickExitNode(en.Nodes, strings.Join(args, " "))
		if err != nil {
			return err
		}
		return setExitNode(cfg, n.Tailnet, cmpOr(n.FQDN, n.ID))
	case "off", "none":
		return setExitNode(cfg, "", "")
	}
	return fmt.Errorf("usage: tailmux exit-node [list [filter] | use <node> | off]")
}

func listExitNodes(en exitNodes, filter string) error {
	if c := en.Current; c != nil {
		state := "in use"
		if !c.Active {
			state = "NOT in use, traffic is blocked: " + c.Error
		}
		fmt.Printf("exit node: %s via %s (%s)\n\n", cmpOr(c.Name, c.Node), c.Tailnet, state)
	} else {
		fmt.Printf("exit node: none (traffic outside your tailnets goes direct)\n\n")
	}
	if len(en.Nodes) == 0 {
		fmt.Println("no device in your tailnets offers itself as an exit node")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, " \tNAME\tTAILNET\tLOCATION\tSTATUS")
	shown := 0
	for _, n := range en.Nodes {
		if filter != "" && !exitMatches(n, filter, true) {
			continue
		}
		shown++
		sel, st, loc := " ", "offline", ""
		if n.Selected {
			sel = "*"
		}
		if n.Online {
			st = "online"
		}
		if n.Mullvad && !n.Online {
			st = "" // Mullvad's nodes don't report presence
		}
		if l := n.Location; l != nil {
			loc = strings.TrimPrefix(l.City+", "+l.Country, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", sel, n.Name, n.Tailnet, loc, st)
	}
	tw.Flush()
	if shown == 0 {
		fmt.Printf("nothing matches %q\n", filter)
	}
	return nil
}

// exitMatches: name, MagicDNS name, name.tailnet, IP or ID; with loose,
// also substrings and places (country, country code, city).
func exitMatches(n mux.ExitNodeInfo, q string, loose bool) bool {
	q = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(q), "."))
	exact := []string{n.Name, n.FQDN, n.Name + "." + n.Tailnet, n.ID}
	exact = append(exact, n.IPs...)
	for _, s := range exact {
		if strings.EqualFold(s, q) {
			return true
		}
	}
	if !loose {
		return false
	}
	if l := n.Location; l != nil {
		for _, s := range []string{l.Country, l.CountryCode, l.City, l.CityCode} {
			if s != "" && strings.EqualFold(s, q) {
				return true
			}
		}
		if strings.Contains(strings.ToLower(l.Country+" "+l.City), q) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(n.FQDN), q)
}

// pickExitNode finds the one exit node q means. A place ("se", "Sweden",
// "Stockholm") means the best node there, like the official client's
// "best available": the highest priority, online ones first.
func pickExitNode(nodes []mux.ExitNodeInfo, q string) (mux.ExitNodeInfo, error) {
	var exact, placed []mux.ExitNodeInfo
	for _, n := range nodes {
		switch {
		case exitMatches(n, q, false):
			exact = append(exact, n)
		case n.Location != nil && exitMatches(n, q, true):
			placed = append(placed, n)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		var names []string
		for _, n := range exact {
			names = append(names, n.Name+"."+n.Tailnet)
		}
		return mux.ExitNodeInfo{}, fmt.Errorf("%q is in several tailnets; say which: %s", q, strings.Join(names, ", "))
	}
	if len(placed) == 0 {
		return mux.ExitNodeInfo{}, fmt.Errorf("no exit node matches %q (see `tailmux exit-node list`)", q)
	}
	best := placed[0]
	for _, n := range placed[1:] {
		if (n.Online && !best.Online) || (n.Online == best.Online && n.Location.Priority > best.Location.Priority) {
			best = n
		}
	}
	return best, nil
}

func setExitNode(cfg *mux.Config, tailnet, node string) error {
	body, _ := json.Marshal(mux.ExitNodeConfig{Tailnet: tailnet, Node: node})
	req, _ := http.NewRequest("PUT", "http://"+cfg.HTTP+"/exit-node", bytes.NewReader(body))
	req.Header.Set("X-Tailmux", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return fmt.Errorf("is `tailmux up` running? %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	var r struct {
		Current *mux.ExitStatus `json:"current"`
	}
	json.Unmarshal(b, &r)
	switch c := r.Current; {
	case c == nil:
		fmt.Println("exit node off; traffic outside your tailnets goes direct")
	case c.Active:
		fmt.Printf("using %s (%s) as the exit node\n", cmpOr(c.Name, c.Node), c.Tailnet)
	default:
		fmt.Printf("exit node set to %s (%s), but it isn't usable yet: %s\n", cmpOr(c.Name, c.Node), c.Tailnet, c.Error)
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
