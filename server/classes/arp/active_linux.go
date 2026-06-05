//go:build linux

package arp

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/mdlayher/arp"
)

func init() { activeSupported = true }

// defaultActiveProbe actively sweeps one interface's subnet using raw ARP
// (mdlayher/arp over AF_PACKET). Requires root.
var defaultActiveProbe probeFunc = linuxProbe

func linuxProbe(ctx context.Context, sub ifaceSubnet, targets []netip.Addr,
	rate int, timeout time.Duration) ([]rawEntry, error) {

	c, err := arp.Dial(sub.iface)
	if err != nil {
		return nil, err // e.g. no IPv4, or EPERM when not root — caller continues
	}
	defer c.Close()

	if rate <= 0 {
		rate = 1
	}
	spacing := time.Second / time.Duration(rate)
	// Read deadline must outlast the full send phase plus the listen window.
	deadline := time.Now().Add(time.Duration(len(targets))*spacing + timeout)
	_ = c.SetReadDeadline(deadline)

	var (
		mu      sync.Mutex
		results = map[string]string{} // ip -> mac
		done    = make(chan struct{})
	)
	go func() {
		defer close(done)
		for {
			pkt, _, err := c.Read()
			if err != nil {
				return // deadline reached or socket closed
			}
			if pkt.Operation != arp.OperationReply {
				continue
			}
			mac := normalizeMAC(pkt.SenderHardwareAddr.String())
			if !isUsableMAC(mac) {
				continue
			}
			mu.Lock()
			results[pkt.SenderIP.String()] = mac
			mu.Unlock()
		}
	}()

	// Rate-limited send of one ARP request per target.
	ticker := time.NewTicker(spacing)
	defer ticker.Stop()
send:
	for _, t := range targets {
		select {
		case <-ctx.Done():
			break send
		case <-ticker.C:
		}
		_ = c.Request(t) // ignore per-host send errors
	}

	<-done // wait for the read deadline to drain replies

	mu.Lock()
	defer mu.Unlock()
	out := make([]rawEntry, 0, len(results))
	for ip, mac := range results {
		out = append(out, rawEntry{ip: ip, mac: mac, iface: sub.iface.Name})
	}
	return out, nil
}
