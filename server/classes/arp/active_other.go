//go:build !linux

package arp

import (
	"context"
	"net/netip"
	"time"
)

func init() { activeSupported = false }

// defaultActiveProbe is a stub on non-Linux platforms; active scanning falls
// back to the passive ARP-cache reader.
var defaultActiveProbe probeFunc = func(context.Context, ifaceSubnet, []netip.Addr, int, time.Duration) ([]rawEntry, error) {
	return nil, errActiveUnsupported
}
