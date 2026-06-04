package main

import (
	"flag"
	"fmt"

	"github.com/fatih/color"
)

// appVersion mirrors the API version documented in openapi.yml.
const appVersion = "1.6.0"

// usageExample is a documented example invocation shown in --help.
type usageExample struct {
	desc    string
	command string
}

var usageExamples = []usageExample{
	{"Run on the default address (127.0.0.1:6868)", "sudo pingmon"},
	{"Listen on all interfaces, custom port", "sudo pingmon --host 0.0.0.0 --port 8888"},
	{"Store data in a fixed location", "sudo pingmon --datafolder /var/lib/pingmon"},
	{"Require token auth (repeatable) and enable verbose logging", "sudo pingmon --token 3f2504e0-4f89-41d3-9a0c-0305e82c3301 --debug"},
	{"Name this instance and keep raw results for 30 days", "sudo pingmon --name edge-eu-1 --raw-retain 720h"},
	{"Use a config file (see: pingmon config)", "sudo pingmon --config /etc/pingmon.conf"},
	{"Freeze data / demo (view-only, live data)", "sudo pingmon --readonly"},
}

// printUsage renders a colorized help screen. Colors auto-disable when output is
// not a terminal or NO_COLOR is set (handled by fatih/color).
func printUsage() {
	out := color.Output

	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	header := color.New(color.FgYellow, color.Bold).SprintFunc()
	flagName := color.New(color.FgGreen, color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()
	cmd := color.New(color.FgHiCyan).SprintFunc()
	comment := color.New(color.Faint, color.Italic).SprintFunc()

	fmt.Fprintf(out, "%s %s\n", title("PingMon Server"), dim("v"+appVersion))
	fmt.Fprintln(out, "Real-time ICMP ping monitoring with long-term SQLite analytics.")
	fmt.Fprintln(out)

	fmt.Fprintln(out, header("USAGE"))
	fmt.Fprintf(out, "  %s [flags]            run the server (default)\n", "pingmon")
	fmt.Fprintf(out, "  %s <command> [args]   run a CLI command\n\n", "pingmon")
	fmt.Fprintf(out, "  %s\n\n", dim("Requires root/administrator privileges for ICMP."))

	if len(commandRegistry) > 0 {
		fmt.Fprintln(out, header("COMMANDS"))
		for _, c := range commandRegistry {
			fmt.Fprintf(out, "  %s\n      %s\n", flagName(c.name), c.summary)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, header("FLAGS"))
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		name := flagName("--" + f.Name)
		if f.DefValue != "" {
			fmt.Fprintf(out, "  %s %s\n", name, dim("(default "+f.DefValue+")"))
		} else {
			fmt.Fprintf(out, "  %s\n", name)
		}
		fmt.Fprintf(out, "      %s\n", f.Usage)
	})
	fmt.Fprintln(out)

	fmt.Fprintln(out, header("EXAMPLES"))
	for _, e := range usageExamples {
		fmt.Fprintf(out, "  %s\n  %s\n\n", comment("# "+e.desc), cmd(e.command))
	}

	fmt.Fprintf(out, "%s %s\n", dim("API reference:"), "openapi.yml")
}
