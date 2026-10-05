package main

import (
	"testing"

	"github.com/GrowlyX/tailmux/internal/mux"
)

func TestPickExitNode(t *testing.T) {
	at := func(country, code, city string, prio int) *mux.Location {
		return &mux.Location{Country: country, CountryCode: code, City: city, Priority: prio}
	}
	nodes := []mux.ExitNodeInfo{
		{Tailnet: "home", Name: "nas", FQDN: "nas.tail1.ts.net", IPs: []string{"100.64.0.5"}},
		{Tailnet: "work", Name: "nas", FQDN: "nas.tail2.ts.net"},
		{Tailnet: "home", Name: "pi", FQDN: "pi.tail1.ts.net"},
		{Tailnet: "home", Name: "se-sto-1", FQDN: "se-sto-1.mullvad.ts.net", Mullvad: true, Location: at("Sweden", "SE", "Stockholm", 20)},
		{Tailnet: "home", Name: "se-sto-2", FQDN: "se-sto-2.mullvad.ts.net", Mullvad: true, Location: at("Sweden", "SE", "Stockholm", 80)},
		{Tailnet: "home", Name: "se-got-1", FQDN: "se-got-1.mullvad.ts.net", Mullvad: true, Location: at("Sweden", "SE", "Gothenburg", 90), Online: false},
		{Tailnet: "home", Name: "jp-tyo-1", FQDN: "jp-tyo-1.mullvad.ts.net", Mullvad: true, Location: at("Japan", "JP", "Tokyo", 10)},
	}
	for q, want := range map[string]string{
		"pi":                      "pi.tail1.ts.net",
		"nas.work":                "nas.tail2.ts.net",
		"100.64.0.5":              "nas.tail1.ts.net",
		"se-sto-1.mullvad.ts.net": "se-sto-1.mullvad.ts.net",
		"se":                      "se-got-1.mullvad.ts.net", // best in the country
		"Stockholm":               "se-sto-2.mullvad.ts.net", // best in the city
		"tokyo":                   "jp-tyo-1.mullvad.ts.net",
	} {
		got, err := pickExitNode(nodes, q)
		if err != nil || got.FQDN != want {
			t.Errorf("%q: got %q, %v; want %q", q, got.FQDN, err, want)
		}
	}
	for _, q := range []string{"nas", "nowhere"} {
		if got, err := pickExitNode(nodes, q); err == nil {
			t.Errorf("%q: got %q, want an error", q, got.FQDN)
		}
	}
}
