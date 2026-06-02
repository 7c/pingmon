// Package arp discovers neighbors by reading the operating system's ARP /
// neighbor table across all interfaces, resolving names, and keeping the result
// in memory. It is used both by the server (--arpscan, exposed at GET /api/arp)
// and by the `pingmon arp` CLI command.
package arp

import (
	"context"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one discovered neighbor, accumulated across all scans. Entries are
// never forgotten: each scan that sees an IP updates LastSeen and increments
// Count while preserving FirstSeen.
type Entry struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`       // hardware (ARP) address (latest observed)
	Name      string    `json:"name"`      // reverse-DNS name, best effort
	Interface string    `json:"interface"` // interface it was seen on (latest observed)
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Count     int       `json:"count"` // number of scans this IP was seen in
}

// rawEntry is a parsed ARP-table row before name resolution.
type rawEntry struct {
	ip    string
	mac   string
	iface string
}

// Scanner periodically reads the ARP table and caches enriched entries.
type Scanner struct {
	interval time.Duration

	mu      sync.RWMutex
	entries map[string]Entry // keyed by IP
	resolve bool

	done chan struct{}
	wg   sync.WaitGroup
}

// New creates a Scanner with the given scan interval (used by Start; ignored by
// one-shot ScanOnce). Name resolution is on by default.
func New(interval time.Duration) *Scanner {
	return &Scanner{
		interval: interval,
		entries:  make(map[string]Entry),
		resolve:  true,
		done:     make(chan struct{}),
	}
}

// SetResolve toggles reverse-DNS name resolution.
func (s *Scanner) SetResolve(v bool) { s.resolve = v }

// Start launches the periodic scan loop.
func (s *Scanner) Start() {
	s.wg.Add(1)
	go s.loop()
}

// Stop ends the scan loop and waits for it to exit.
func (s *Scanner) Stop() {
	close(s.done)
	s.wg.Wait()
}

func (s *Scanner) loop() {
	defer s.wg.Done()

	if _, err := s.ScanOnce(); err != nil {
		log.Printf("[ARP] initial scan failed: %v", err)
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if _, err := s.ScanOnce(); err != nil {
				log.Printf("[ARP] scan failed: %v", err)
			}
		}
	}
}

// ScanOnce reads the ARP table once, resolves names for new IPs, merges the
// results into the cache, and returns the full sorted snapshot.
func (s *Scanner) ScanOnce() ([]Entry, error) {
	raws, err := readARPTable()
	if err != nil {
		return nil, err
	}

	// Snapshot known names so DNS resolution happens outside the lock.
	s.mu.RLock()
	known := make(map[string]string, len(s.entries))
	for ip, e := range s.entries {
		known[ip] = e.Name
	}
	s.mu.RUnlock()

	names := make(map[string]string, len(raws))
	for _, r := range raws {
		name := known[r.ip]
		if name == "" && s.resolve {
			name = resolveName(r.ip)
		}
		names[r.ip] = name
	}

	s.merge(raws, names)
	return s.Entries(), nil
}

// merge accumulates a scan's raw entries into the cache, preserving FirstSeen,
// bumping Count, and updating LastSeen / latest MAC+interface. names is an
// optional ip->name map; entries keep an existing non-empty name.
func (s *Scanner) merge(raws []rawEntry, names map[string]string) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range raws {
		e, existed := s.entries[r.ip]
		e.IP = r.ip
		e.MAC = r.mac         // latest observed MAC
		e.Interface = r.iface // latest observed interface
		e.LastSeen = now
		e.Count++
		if !existed {
			e.FirstSeen = now
		}
		if e.Name == "" {
			e.Name = names[r.ip]
		}
		s.entries[r.ip] = e
	}
}

// Entries returns a snapshot of all cached entries, sorted by IP.
func (s *Scanner) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return lessIP(out[i].IP, out[j].IP) })
	return out
}

// resolveName does a bounded best-effort reverse-DNS lookup.
func resolveName(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// lessIP orders IPs numerically when possible, falling back to string order.
func lessIP(a, b string) bool {
	ipa, ipb := net.ParseIP(a), net.ParseIP(b)
	if ipa != nil && ipb != nil {
		a4, b4 := ipa.To4(), ipb.To4()
		if a4 != nil && b4 != nil {
			for i := 0; i < 4; i++ {
				if a4[i] != b4[i] {
					return a4[i] < b4[i]
				}
			}
			return false
		}
	}
	return a < b
}

// normalizeMAC lowercases and zero-pads each octet (so "a:b:c:1:2:3" becomes
// "0a:0b:0c:01:02:03").
func normalizeMAC(mac string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.Join(parts, ":")
}

// isUsableMAC filters incomplete/broadcast entries.
func isUsableMAC(mac string) bool {
	switch mac {
	case "", "00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "(incomplete)":
		return false
	}
	return strings.Count(mac, ":") == 5
}
