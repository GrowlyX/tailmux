package mux

import (
	"slices"
	"testing"
)

func TestMullvadHiddenFromDevices(t *testing.T) {
	m := New(&Config{Tailnets: []TailnetConfig{{Name: "home"}}}, Options{})
	tn := m.Tailnet("home")
	tn.snap = Snapshot{Name: "home", Running: true, Peers: []Peer{
		{Name: "nas", FQDN: "nas.tail1.ts.net", Exit: true, Online: true},
		{Name: "laptop", FQDN: "laptop.tail1.ts.net"},
		{Name: "us-nyc-1", FQDN: "us-nyc-1.mullvad.ts.net", Exit: true, Online: true},
		{Name: "se-sto-1", FQDN: "se-sto-1.mullvad.ts.net", Exit: true},
	}}
	m.rebuild()

	var peers []string
	for _, p := range m.Peers() {
		peers = append(peers, p.Name)
	}
	if want := []string{"nas", "laptop"}; !slices.Equal(peers, want) {
		t.Errorf("Peers() = %v, want %v", peers, want)
	}

	if s := tn.Status(); s.Peers != 2 || s.Online != 1 {
		t.Errorf("Status() = %d/%d online, want 1/2", s.Online, s.Peers)
	}

	var exits []string
	for _, e := range m.ExitNodes() {
		exits = append(exits, e.Name)
	}
	for _, name := range []string{"nas", "us-nyc-1", "se-sto-1"} {
		if !slices.Contains(exits, name) {
			t.Errorf("ExitNodes() = %v, missing %s", exits, name)
		}
	}
}
