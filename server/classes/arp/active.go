package arp

import (
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"net/netip"
	"time"
)

// Active-scan tunables.
const (
	activeSendRate        = 200             // ARP requests/sec per interface
	activeListenTimeout   = 2 * time.Second // drain replies this long after sending
	activeScanBudget      = 45 * time.Second
	defaultActiveMaxHosts = 256 // ~/24; subnets with more addresses are skipped
)

// errActiveUnsupported is returned by the probe on platforms without active ARP
// support (everything except Linux), so callers can fall back to passive.
var errActiveUnsupported = errors.New("active arp scan not supported on this platform")

// activeSupported is set by the platform build-tagged files.
var activeSupported bool

// probeFunc sends ARP requests to targets on one interface and returns the
// discovered (ip, mac) entries. Implementations live in active_{linux,other}.go.
type probeFunc func(ctx context.Context, sub ifaceSubnet, targets []netip.Addr,
	rate int, timeout time.Duration) ([]rawEntry, error)

// activeProbe is the IO seam; tests override it. Initialized per-platform.
var activeProbe = defaultActiveProbe

// interfacesFunc enumerates interfaces+addrs; tests override it for determinism.
var interfacesFunc = realInterfaces

func realInterfaces() ([]ifaceWithAddrs, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]ifaceWithAddrs, 0, len(ifaces))
	for i := range ifaces {
		addrs, _ := ifaces[i].Addrs()
		out = append(out, ifaceWithAddrs{iface: &ifaces[i], addrs: addrs})
	}
	return out, nil
}

// ifaceWithAddrs decouples interface enumeration from net.Interface.Addrs for tests.
type ifaceWithAddrs struct {
	iface *net.Interface
	addrs []net.Addr
}

// ifaceSubnet is one interface + the IPv4 network to actively sweep.
type ifaceSubnet struct {
	iface  *net.Interface
	cidr   *net.IPNet // masked network
	selfIP net.IP     // the interface's own IPv4 (excluded from targets)
}

type skippedSubnet struct {
	iface  string
	cidr   string
	reason string
}

// activeScanAll sweeps every eligible interface subnet and returns deduped
// rawEntries. Returns errActiveUnsupported on non-Linux so the caller falls back
// to passive.
func activeScanAll(ctx context.Context, maxHosts int) ([]rawEntry, error) {
	if !activeSupported {
		return nil, errActiveUnsupported
	}
	withAddrs, err := interfacesFunc()
	if err != nil {
		return nil, err
	}

	kept, skipped := eligibleSubnets(withAddrs, maxHosts)
	for _, s := range skipped {
		if s.reason == "oversized" {
			log.Printf("[ARP] skipping active scan on %s %s: subnet too large (cap %d hosts)", s.iface, s.cidr, maxHosts)
		}
	}

	var all []rawEntry
	for _, sub := range kept {
		targets := hostsInCIDR(sub.cidr, sub.selfIP)
		log.Printf("[ARP] active scan on %s %s (%d hosts)", sub.iface.Name, sub.cidr.String(), len(targets))
		entries, err := activeProbe(ctx, sub, targets, activeSendRate, activeListenTimeout)
		if err != nil {
			if errors.Is(err, errActiveUnsupported) {
				return nil, err
			}
			log.Printf("[ARP] active scan on %s failed (continuing): %v", sub.iface.Name, err)
			continue
		}
		all = append(all, entries...)
	}
	return dedupRaws(all), nil
}

// eligibleSubnets selects interfaces worth actively sweeping: up, not loopback,
// not point-to-point, with an IPv4 network whose address count is <= maxHosts.
func eligibleSubnets(ifaces []ifaceWithAddrs, maxHosts int) (kept []ifaceSubnet, skipped []skippedSubnet) {
	for _, ia := range ifaces {
		f := ia.iface.Flags
		if f&net.FlagUp == 0 || f&net.FlagLoopback != 0 || f&net.FlagPointToPoint != 0 {
			continue
		}
		for _, a := range ia.addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			count := subnetAddrCount(ipnet)
			if count == 0 {
				continue
			}
			cidr := &net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask).To4(), Mask: ipnet.Mask}
			if count > maxHosts {
				skipped = append(skipped, skippedSubnet{iface: ia.iface.Name, cidr: cidr.String(), reason: "oversized"})
				continue
			}
			kept = append(kept, ifaceSubnet{iface: ia.iface, cidr: cidr, selfIP: ipnet.IP.To4()})
		}
	}
	return kept, skipped
}

// subnetAddrCount returns the number of addresses in an IPv4 network, or 0 if
// the network is not IPv4.
func subnetAddrCount(n *net.IPNet) int {
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return 0
	}
	return 1 << uint(32-ones)
}

// hostsInCIDR returns the assignable host IPs of an IPv4 network, excluding the
// network and broadcast addresses (and skipSelf). For /31 and /32 it returns all
// addresses in range.
func hostsInCIDR(n *net.IPNet, skipSelf net.IP) []netip.Addr {
	ip4 := n.IP.To4()
	if ip4 == nil {
		return nil
	}
	mask := net.IP(n.Mask).To4()
	if mask == nil {
		return nil
	}
	base := binary.BigEndian.Uint32(ip4) & binary.BigEndian.Uint32(mask)
	bcast := base | ^binary.BigEndian.Uint32(mask)

	lo, hi := base, bcast
	if bcast-base > 1 { // exclude network + broadcast for /30 and larger
		lo, hi = base+1, bcast-1
	}

	var self uint32
	if s := skipSelf.To4(); s != nil {
		self = binary.BigEndian.Uint32(s)
	}

	out := make([]netip.Addr, 0, hi-lo+1)
	for u := lo; ; u++ {
		if u != self {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], u)
			out = append(out, netip.AddrFrom4(b))
		}
		if u == hi {
			break
		}
	}
	return out
}

// dedupRaws keeps one rawEntry per IP, first occurrence winning (active entries
// are passed before passive, so active MACs take precedence).
func dedupRaws(in []rawEntry) []rawEntry {
	seen := make(map[string]struct{}, len(in))
	out := make([]rawEntry, 0, len(in))
	for _, r := range in {
		if _, ok := seen[r.ip]; ok {
			continue
		}
		seen[r.ip] = struct{}{}
		out = append(out, r)
	}
	return out
}
