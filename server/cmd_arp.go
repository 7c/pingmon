package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"

	"github.com/7c/pingmon/classes/arp"
)

func init() {
	registerCommand(command{
		name:    "arp",
		summary: "Scan the ARP table on all interfaces and print it (no server)",
		run:     runArpCmd,
	})
}

// runArpCmd performs a one-shot ARP scan and prints the results.
func runArpCmd(args []string) int {
	fs := flag.NewFlagSet("arp", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output JSON instead of a table")
	noResolve := fs.Bool("no-resolve", false, "Skip reverse-DNS name resolution")
	fs.Usage = func() {
		fmt.Fprintf(color.Output, "Usage: pingmon arp [--json] [--no-resolve]\n\nScans the OS ARP/neighbor table on all interfaces and prints discovered hosts.\n")
	}
	_ = fs.Parse(args)

	scanner := arp.New(0)
	scanner.SetResolve(!*noResolve)

	entries, err := scanner.ScanOnce()
	if err != nil {
		fmt.Fprintf(color.Error, "arp scan failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		return 0
	}

	printArpTable(entries)
	return 0
}

// printArpTable renders a colorized table of ARP entries.
func printArpTable(entries []arp.Entry) {
	out := color.Output
	header := color.New(color.FgCyan, color.Bold).SprintfFunc()
	dim := color.New(color.Faint).SprintFunc()

	if len(entries) == 0 {
		fmt.Fprintln(out, dim("No ARP entries found."))
		return
	}

	fmt.Fprintf(out, "%s\n", header("%-16s %-17s %-6s %-8s %s", "IP", "MAC", "SEEN", "IFACE", "NAMES"))
	for _, e := range entries {
		fmt.Fprintf(out, "%-16s %-17s %-6d %-8s %s\n",
			e.IP, e.MAC, e.Count, e.Interface, dim(strings.Join(e.Names, ", ")))
	}
	fmt.Fprintf(out, "\n%s\n", dim(fmt.Sprintf("%d host(s)", len(entries))))
}
