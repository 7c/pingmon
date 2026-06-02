package arp

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// readARPTable reads the OS neighbor/ARP table across all interfaces. On Linux
// it parses /proc/net/arp directly; on other Unixes it parses `arp -an`.
func readARPTable() ([]rawEntry, error) {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/net/arp")
		if err != nil {
			return nil, fmt.Errorf("read /proc/net/arp: %w", err)
		}
		return parseProcNetARP(string(data)), nil
	}

	out, err := exec.Command("arp", "-an").Output()
	if err != nil {
		return nil, fmt.Errorf("run arp -an: %w", err)
	}
	return parseArpCommand(string(out)), nil
}

// parseProcNetARP parses the Linux /proc/net/arp table:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.1.1      0x1         0x2         ab:cd:ef:12:34:56     *        eth0
func parseProcNetARP(data string) []rawEntry {
	var out []rawEntry
	for i, line := range strings.Split(data, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		mac := normalizeMAC(f[3])
		if !isUsableMAC(mac) {
			continue
		}
		out = append(out, rawEntry{ip: f[0], mac: mac, iface: f[5]})
	}
	return out
}

// arpCmdRe matches BSD/macOS `arp -an` lines, e.g.
//
//	? (192.168.1.1) at ab:cd:ef:12:34:56 on en0 ifscope [ethernet]
var arpCmdRe = regexp.MustCompile(`\(([0-9.]+)\) at ([0-9a-fA-F:]+|\(incomplete\)) on (\S+)`)

// parseArpCommand parses `arp -an` output.
func parseArpCommand(output string) []rawEntry {
	var out []rawEntry
	for _, line := range strings.Split(output, "\n") {
		m := arpCmdRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mac := normalizeMAC(m[2])
		if !isUsableMAC(mac) {
			continue
		}
		out = append(out, rawEntry{ip: m[1], mac: mac, iface: m[3]})
	}
	return out
}
