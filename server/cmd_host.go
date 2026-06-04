package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/fatih/color"

	"github.com/7c/pingmon/classes/store"
)

func init() {
	registerCommand(command{
		name:    "host",
		summary: "Manage monitored hosts in the data store: host add|list (no server/token)",
		run:     runHostCmd,
	})
}

func runHostCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(color.Error, "usage: pingmon host <add|list> [args]")
		return 2
	}
	switch args[0] {
	case "add":
		return hostAdd(args[1:])
	case "list", "ls":
		return hostList(args[1:])
	default:
		fmt.Fprintf(color.Error, "unknown host subcommand: %s\n", args[0])
		return 2
	}
}

func hostAdd(args []string) int {
	fs := flag.NewFlagSet("host add", flag.ExitOnError)
	df := addStoreFlags(fs)
	interval := fs.Int("interval", store.DefaultIntervalMs, "ping interval (ms)")
	timeout := fs.Int("timeout", store.DefaultTimeoutMs, "ping timeout (ms)")
	size := fs.Int("size", store.DefaultPacketSize, "packet size (bytes)")
	name := fs.String("name", "", "display name (applied to all given hosts)")
	tagsCSV := fs.String("tags", "", "comma-separated tags")
	fs.Usage = func() {
		fmt.Fprintln(color.Output, "usage: pingmon host add [--interval ms] [--timeout ms] [--size n] [--name N] [--tags a,b] <ip> [<ip>...]")
	}
	_ = fs.Parse(args)

	ips := fs.Args()
	if len(ips) == 0 {
		fmt.Fprintln(color.Error, "host add: at least one ip/hostname is required")
		return 2
	}

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "host add: %v\n", err)
		return 1
	}
	defer st.Close()

	cfg := clampConfig(store.HostConfig{IntervalMs: *interval, TimeoutMs: *timeout, PacketSize: *size})
	var tags []string
	if *tagsCSV != "" {
		tags = splitNonEmpty(*tagsCSV, ",")
	}

	rc := 0
	for _, ip := range ips {
		if err := st.AddHostWithConfig(ip, cfg); err != nil {
			fmt.Fprintf(color.Error, "  %s: %v\n", ip, err)
			rc = 1
			continue
		}
		if *name != "" {
			_ = st.UpdateHostMeta(ip, *name, "")
		}
		if len(tags) > 0 {
			_ = st.SetHostTags(ip, tags)
		}
		fmt.Fprintf(color.Output, "added %s (interval %dms)\n", ip, cfg.IntervalMs)
	}
	fmt.Fprintln(color.Output, color.New(color.Faint).Sprint("Note: a running server picks up new hosts on its next start."))
	return rc
}

func hostList(args []string) int {
	fs := flag.NewFlagSet("host list", flag.ExitOnError)
	df := addStoreFlags(fs)
	_ = fs.Parse(args)

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "host list: %v\n", err)
		return 1
	}
	defer st.Close()

	hosts, err := st.ListHostsFull()
	if err != nil {
		fmt.Fprintf(color.Error, "host list: %v\n", err)
		return 1
	}

	out := color.Output
	header := color.New(color.FgCyan, color.Bold).SprintfFunc()
	dim := color.New(color.Faint).SprintFunc()
	if len(hosts) == 0 {
		fmt.Fprintln(out, dim("No hosts."))
		return 0
	}
	fmt.Fprintf(out, "%s\n", header("%-22s %-9s %-20s %s", "IP", "INTERVAL", "NAME", "TAGS"))
	for _, h := range hosts {
		fmt.Fprintf(out, "%-22s %-9s %-20s %s\n",
			h.IP, fmt.Sprintf("%dms", h.Config.IntervalMs), h.DisplayName, dim(strings.Join(h.Tags, ", ")))
	}
	fmt.Fprintf(out, "\n%s\n", dim(fmt.Sprintf("%d host(s)", len(hosts))))
	return 0
}
