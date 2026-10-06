//go:build windows

package tun

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// The Wintun adapter's name. wintun.dll must sit next to tailmux.exe;
// the Windows installer puts it there.
const defaultTUNName = "tailmux"

type windowsOS struct{}

func newOSConfig() (osConfig, error) {
	w := windowsOS{}
	w.removeNRPT() // leftovers from a crash
	return w, nil
}

// luidOf finds the adapter by name; it can take a moment to appear.
func luidOf(ifname string) (winipcfg.LUID, error) {
	var lastErr error
	for range 50 {
		ifc, err := net.InterfaceByName(ifname)
		if err == nil {
			return winipcfg.LUIDFromIndex(uint32(ifc.Index))
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("adapter %s: %w", ifname, lastErr)
}

func (windowsOS) up(ifname string, gw netip.Addr, fake netip.Prefix, mtu int) error {
	luid, err := luidOf(ifname)
	if err != nil {
		return err
	}
	// The address carries the whole fake range, so it is on-link: no
	// separate route needed for it.
	if err := luid.SetIPAddressesForFamily(windows.AF_INET, []netip.Prefix{netip.PrefixFrom(gw, fake.Bits())}); err != nil {
		return fmt.Errorf("set address: %w", err)
	}
	row, err := luid.IPInterface(windows.AF_INET)
	if err != nil {
		return err
	}
	row.NLMTU = uint32(mtu)
	row.UseAutomaticMetric = false
	row.Metric = 5 // prefer it over physical adapters for the routes it has
	return row.Set()
}

func nextHop(p netip.Prefix) netip.Addr {
	if p.Addr().Is4() {
		return netip.IPv4Unspecified()
	}
	return netip.IPv6Unspecified()
}

func (windowsOS) addRoute(ifname string, p netip.Prefix) error {
	luid, err := luidOf(ifname)
	if err != nil {
		return err
	}
	err = luid.AddRoute(p, nextHop(p), 0)
	if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
		return nil
	}
	return err
}

func (windowsOS) delRoute(ifname string, p netip.Prefix) error {
	luid, err := luidOf(ifname)
	if err != nil {
		return err
	}
	return luid.DeleteRoute(p, nextHop(p))
}

// setExit relies on tailscale's netns binding tsnet's sockets to the
// default route's interface (IP_UNICAST_IF), as the official client does.
func (w windowsOS) setExit(ifname string, on bool) error {
	return setExitRoutes(on,
		func(p netip.Prefix) error { return w.addRoute(ifname, p) },
		func(p netip.Prefix) error { return w.delRoute(ifname, p) })
}

// nrptName is the NRPT form of a domain: ".example.com", or "." for
// every name (the exit node catch-all).
func nrptName(d string) string {
	if d == "." {
		return d
	}
	return "." + d
}

// Split DNS on Windows is the Name Resolution Policy Table: registry
// rules saying "names under these suffixes go to this server". The
// official Tailscale client uses the same mechanism.
const (
	nrptBase = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	// Our rule keys are {tailmux-NN}; Windows reads every subkey.
	nrptPrefix = "{tailmux-"
	// Windows ignores rules with more names than this.
	nrptMaxNames = 50
)

func (w windowsOS) setDNS(_ string, domains, _ []string, server netip.Addr) (bool, error) {
	w.removeNRPT()
	for i := 0; i*nrptMaxNames < len(domains); i++ {
		chunk := domains[i*nrptMaxNames : min(len(domains), (i+1)*nrptMaxNames)]
		names := make([]string, len(chunk))
		for j, d := range chunk {
			names[j] = nrptName(d)
		}
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, fmt.Sprintf(`%s\%s%02d}`, nrptBase, nrptPrefix, i), registry.SET_VALUE)
		if err != nil {
			return false, fmt.Errorf("NRPT: %w", err)
		}
		err = errors.Join(
			k.SetDWordValue("Version", 2),
			k.SetStringsValue("Name", names),
			k.SetStringValue("GenericDNSServers", server.String()),
			k.SetDWordValue("ConfigOptions", 8), // use GenericDNSServers
			k.SetStringValue("IPSECCARestriction", ""),
		)
		k.Close()
		if err != nil {
			return false, fmt.Errorf("NRPT: %w", err)
		}
	}
	refreshPolicy()
	w.flushDNS()
	return true, nil
}

func (windowsOS) dnsIntact(_ string, domains, _ []string) bool {
	if len(domains) == 0 {
		return true
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptBase+`\`+nrptPrefix+`00}`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	names, _, err := k.GetStringsValue("Name")
	if err != nil {
		return false
	}
	for _, n := range names {
		if strings.EqualFold(n, nrptName(domains[0])) {
			return true
		}
	}
	return false
}

func (windowsOS) removeNRPT() {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptBase, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()
	removed := false
	for _, s := range subs {
		if strings.HasPrefix(s, nrptPrefix) {
			registry.DeleteKey(registry.LOCAL_MACHINE, nrptBase+`\`+s)
			removed = true
		}
	}
	if removed {
		refreshPolicy()
	}
}

var (
	dnsapi                    = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsFlushResolverCache = dnsapi.NewProc("DnsFlushResolverCache")
	userenv                   = windows.NewLazySystemDLL("userenv.dll")
	procRefreshPolicyEx       = userenv.NewProc("RefreshPolicyEx")
)

// refreshPolicy makes the DNS client pick up NRPT changes now rather than
// at its next policy refresh.
func refreshPolicy() {
	if procRefreshPolicyEx.Find() == nil {
		const rpForce = 1
		procRefreshPolicyEx.Call(1, rpForce) // machine policy
	}
}

func (windowsOS) flushDNS() {
	if procDnsFlushResolverCache.Find() == nil {
		procDnsFlushResolverCache.Call()
	}
}

func (w windowsOS) close(string) error {
	w.removeNRPT()
	w.flushDNS()
	return nil
}
