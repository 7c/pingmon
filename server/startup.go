package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/fatih/color"
)

// startupInfo holds the values shown in the startup overview banner.
type startupInfo struct {
	name             string
	version          string
	listenAddrs      []string
	dataFolder       string
	dbPath           string
	hostCount        int
	minIntervalMs    int
	configPath       string
	tokens           []string
	rawRetain        time.Duration
	rollupInterval   time.Duration
	debug            bool
	readOnly         bool
	arpScan          bool
	arpInterval      time.Duration
	arpActive        bool
	arpActiveEvery   time.Duration
	arpActiveMaxHost int
}

// printStartupOverview prints a colorized at-a-glance summary to stdout. Colors
// auto-disable when stdout is not a terminal or NO_COLOR is set.
func printStartupOverview(info startupInfo) {
	out := color.Output

	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	label := color.New(color.Faint).SprintFunc()
	val := color.New(color.FgWhite, color.Bold).SprintFunc()
	on := color.New(color.FgGreen, color.Bold).SprintFunc()
	off := color.New(color.Faint).SprintFunc()
	warn := color.New(color.FgYellow, color.Bold).SprintFunc()

	row := func(k, v string) {
		fmt.Fprintf(out, "  %s %s\n", label(fmt.Sprintf("%-12s", k)), v)
	}

	fmt.Fprintln(out)
	mode := ""
	if info.readOnly {
		mode = " " + warn("[READ-ONLY]")
	}
	fmt.Fprintf(out, "%s %s%s\n", title("● PingMon"), label("v"+info.version), mode)
	row("name", val(info.name))
	if info.configPath != "" {
		row("config", val(info.configPath))
	}
	for i, a := range info.listenAddrs {
		k := "listen"
		if i > 0 {
			k = ""
		}
		row(k, val("http://"+a))
	}
	row("data", val(info.dataFolder))
	row("database", val(info.dbPath))
	row("hosts", val(fmt.Sprintf("%d monitored", info.hostCount)))
	row("min interval", val(fmt.Sprintf("%dms", info.minIntervalMs)))

	// Auth.
	if len(info.tokens) == 0 {
		row("auth", off("disabled — API is open"))
	} else {
		masked := make([]string, len(info.tokens))
		for i, t := range info.tokens {
			masked[i] = maskToken(t)
		}
		row("auth", on(fmt.Sprintf("%d token(s): ", len(info.tokens)))+label(strings.Join(masked, ", ")))
	}

	row("cors", val("fully open (any origin)"))

	if info.arpScan {
		mode := fmt.Sprintf("passive %s", info.arpInterval)
		if info.arpActive {
			mode += fmt.Sprintf(", active %s (cap %d hosts)", info.arpActiveEvery, info.arpActiveMaxHost)
		}
		row("arpscan", on("on")+label(" "+mode))
	} else {
		row("arpscan", off("off"))
	}

	retain := "forever"
	if info.rawRetain > 0 {
		retain = info.rawRetain.String()
	}
	row("retention", val(fmt.Sprintf("raw %s, rollup every %s", retain, info.rollupInterval)))

	if info.debug {
		row("debug", warn("ON"))
	} else {
		row("debug", off("off"))
	}

	fmt.Fprintln(out)
}
