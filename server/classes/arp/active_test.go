package arp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

func TestHostsInCIDR(t *testing.T) {
	// /30: network .0, hosts .1 .2, broadcast .3 → 2 hosts.
	got := hostsInCIDR(mustCIDR(t, "192.168.1.0/30"), nil)
	if len(got) != 2 || got[0].String() != "192.168.1.1" || got[1].String() != "192.168.1.2" {
		t.Fatalf("/30 hosts wrong: %v", got)
	}

	// /24 → 254 hosts.
	got = hostsInCIDR(mustCIDR(t, "10.0.0.0/24"), nil)
	if len(got) != 254 || got[0].String() != "10.0.0.1" || got[253].String() != "10.0.0.254" {
		t.Fatalf("/24 host count/range wrong: %d", len(got))
	}

	// skipSelf removes the host's own IP.
	got = hostsInCIDR(mustCIDR(t, "10.0.0.0/24"), net.ParseIP("10.0.0.5"))
	if len(got) != 253 {
		t.Fatalf("expected 253 after skipSelf, got %d", len(got))
	}
	for _, a := range got {
		if a.String() == "10.0.0.5" {
			t.Fatalf("skipSelf not excluded")
		}
	}

	// /31 → both addresses are usable.
	got = hostsInCIDR(mustCIDR(t, "192.168.1.4/31"), nil)
	if len(got) != 2 {
		t.Fatalf("/31 should yield 2, got %d", len(got))
	}
}

func TestSubnetAddrCount(t *testing.T) {
	cases := map[string]int{"10.0.0.0/24": 256, "10.0.0.0/25": 128, "10.0.0.0/23": 512, "10.0.0.0/16": 65536}
	for cidr, want := range cases {
		if got := subnetAddrCount(mustCIDR(t, cidr)); got != want {
			t.Errorf("subnetAddrCount(%s)=%d want %d", cidr, got, want)
		}
	}
}

func ifaceAddr(t *testing.T, name string, flags net.Flags, cidr string) ifaceWithAddrs {
	t.Helper()
	ia := ifaceWithAddrs{iface: &net.Interface{Name: name, Flags: flags}}
	if cidr != "" {
		ip, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		ia.addrs = []net.Addr{&net.IPNet{IP: ip, Mask: n.Mask}}
	}
	return ia
}

func TestEligibleSubnets(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast
	ifaces := []ifaceWithAddrs{
		ifaceAddr(t, "wlan0", up, "192.168.8.132/24"),                        // kept
		ifaceAddr(t, "lo", net.FlagUp|net.FlagLoopback, "127.0.0.1/8"),       // loopback skip
		ifaceAddr(t, "down0", net.FlagBroadcast, "10.1.1.5/24"),              // down skip
		ifaceAddr(t, "ppp0", net.FlagUp|net.FlagPointToPoint, "10.2.2.2/32"), // p2p skip
		ifaceAddr(t, "big0", up, "10.0.0.5/16"),                              // oversized skip
	}
	// IPv6-only interface.
	v6 := ifaceWithAddrs{iface: &net.Interface{Name: "v6", Flags: up}}
	if ip, n, err := net.ParseCIDR("fe80::1/64"); err == nil {
		v6.addrs = []net.Addr{&net.IPNet{IP: ip, Mask: n.Mask}}
	} else {
		t.Fatal(err)
	}
	ifaces = append(ifaces, v6)

	kept, skipped := eligibleSubnets(ifaces, 256)
	if len(kept) != 1 || kept[0].iface.Name != "wlan0" || kept[0].cidr.String() != "192.168.8.0/24" {
		t.Fatalf("expected only wlan0 /24 kept, got %+v", kept)
	}
	var oversized bool
	for _, s := range skipped {
		if s.iface == "big0" && s.reason == "oversized" {
			oversized = true
		}
	}
	if !oversized {
		t.Fatalf("expected big0 reported oversized, got %+v", skipped)
	}

	// Raising the cap keeps the /16.
	kept2, _ := eligibleSubnets(ifaces, 100000)
	if len(kept2) != 2 {
		t.Fatalf("expected /24 and /16 kept with high cap, got %d", len(kept2))
	}
}

func TestDedupRaws(t *testing.T) {
	in := []rawEntry{
		{ip: "10.0.0.1", mac: "aa:bb:cc:dd:ee:ff", iface: "wlan0"}, // active
		{ip: "10.0.0.2", mac: "11:22:33:44:55:66", iface: "wlan0"}, // active
		{ip: "10.0.0.1", mac: "99:99:99:99:99:99", iface: "wlan0"}, // passive dup -> dropped
		{ip: "10.0.0.3", mac: "de:ad:be:ef:00:01", iface: "wlan0"}, // passive only
	}
	got := dedupRaws(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 deduped, got %d", len(got))
	}
	if got[0].mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("active should win for 10.0.0.1, got %s", got[0].mac)
	}
}

func TestActiveScanAllUnsupported(t *testing.T) {
	prev := activeSupported
	defer func() { activeSupported = prev }()
	activeSupported = false
	if _, err := activeScanAll(context.Background(), 256); !errors.Is(err, errActiveUnsupported) {
		t.Fatalf("expected errActiveUnsupported, got %v", err)
	}
}

func TestActiveScanAllStubbed(t *testing.T) {
	// Save & restore the seams.
	prevSup, prevProbe, prevIf := activeSupported, activeProbe, interfacesFunc
	defer func() { activeSupported, activeProbe, interfacesFunc = prevSup, prevProbe, prevIf }()

	activeSupported = true
	up := net.FlagUp | net.FlagBroadcast
	interfacesFunc = func() ([]ifaceWithAddrs, error) {
		return []ifaceWithAddrs{
			ifaceAddr(t, "wlan0", up, "192.168.8.132/24"),
			ifaceAddr(t, "big0", up, "10.0.0.5/16"), // oversized -> skipped, probe not called
		}, nil
	}

	var sweptCIDRs []string
	activeProbe = func(_ context.Context, sub ifaceSubnet, targets []netip.Addr, _ int, _ time.Duration) ([]rawEntry, error) {
		sweptCIDRs = append(sweptCIDRs, sub.cidr.String())
		// Return one canned reply for this subnet.
		return []rawEntry{{ip: "192.168.8.1", mac: "98:9c:57:c5:20:b7", iface: sub.iface.Name}}, nil
	}

	got, err := activeScanAll(context.Background(), 256)
	if err != nil {
		t.Fatalf("activeScanAll: %v", err)
	}
	if len(sweptCIDRs) != 1 || sweptCIDRs[0] != "192.168.8.0/24" {
		t.Fatalf("expected only the /24 swept, got %v", sweptCIDRs)
	}
	if len(got) != 1 || got[0].ip != "192.168.8.1" {
		t.Fatalf("unexpected results: %+v", got)
	}

	// A probe returning errActiveUnsupported propagates.
	activeProbe = func(context.Context, ifaceSubnet, []netip.Addr, int, time.Duration) ([]rawEntry, error) {
		return nil, errActiveUnsupported
	}
	if _, err := activeScanAll(context.Background(), 256); !errors.Is(err, errActiveUnsupported) {
		t.Fatalf("expected propagated errActiveUnsupported, got %v", err)
	}
}
