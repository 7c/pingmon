package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
)

func init() {
	registerCommand(command{
		name:    "stats",
		summary: "Show stats about server-side stored state (hosts, groups, results, …)",
		run:     runStatsCmd,
	})
}

func runStatsCmd(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	df := addStoreFlags(fs)
	configP := fs.String("config", defaultConfigPath, "Config file (used to locate a running server)")
	jsonOut := fs.Bool("json", false, "Output JSON")
	_ = fs.Parse(args)

	st, dbPath, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "stats: %v\n", err)
		return 1
	}
	defer st.Close()

	stats, err := st.Stats()
	if err != nil {
		fmt.Fprintf(color.Error, "stats: %v\n", err)
		return 1
	}

	var sizeBytes int64 = -1
	if fi, err := os.Stat(dbPath); err == nil {
		sizeBytes = fi.Size()
	}

	// If a server is running and reachable, fetch its live runtime stats.
	cfg, _, _ := loadConfig(*configP)
	runtimeStats, runtimeURL, running := probeRuntime(cfg)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		out := map[string]interface{}{
			"database":  dbPath,
			"sizeBytes": sizeBytes,
			"stats":     stats,
			"running":   running,
		}
		if running {
			out["runtime"] = runtimeStats
		}
		_ = enc.Encode(out)
		return 0
	}

	out := color.Output
	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	label := color.New(color.Faint).SprintFunc()
	val := color.New(color.FgWhite, color.Bold).SprintFunc()

	row := func(k, v string) { fmt.Fprintf(out, "  %s %s\n", label(fmt.Sprintf("%-14s", k)), v) }

	fmt.Fprintf(out, "\n%s\n", title("● PingMon store"))
	row("database", val(dbPath))
	if sizeBytes >= 0 {
		row("size", val(humanBytes(sizeBytes)))
	}
	row("hosts", val(fmt.Sprintf("%d", stats.Hosts)))
	row("groups", val(fmt.Sprintf("%d", stats.Groups)))
	row("tags", val(fmt.Sprintf("%d", stats.Tags)))
	row("annotations", val(fmt.Sprintf("%d", stats.Annotations)))
	row("results", val(fmt.Sprintf("%d", stats.Results)))
	row("rollups", val(fmt.Sprintf("%d hourly, %d daily", stats.RollupHourly, stats.RollupDaily)))
	if stats.Oldest != nil && stats.Newest != nil {
		row("data span", val(fmt.Sprintf("%s → %s",
			stats.Oldest.Format("2006-01-02 15:04"), stats.Newest.Format("2006-01-02 15:04"))))
	}
	fmt.Fprintln(out)

	if running {
		printRuntimeStats(runtimeStats, runtimeURL)
	} else {
		off := color.New(color.Faint).SprintFunc()
		fmt.Fprintf(out, "  %s\n\n", off("server not running — live runtime stats unavailable (showing stored state only)"))
	}
	return 0
}

// printRuntimeStats renders the live runtime section for a reachable server:
// per-type memory (first/max/current), goroutines, uptime, and read latency.
func printRuntimeStats(rs *RuntimeStats, url string) {
	out := color.Output
	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	label := color.New(color.Faint).SprintFunc()
	val := color.New(color.FgWhite, color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	row := func(k, v string) { fmt.Fprintf(out, "  %s %s\n", label(fmt.Sprintf("%-14s", k)), v) }

	fmt.Fprintf(out, "%s %s\n", title("● PingMon runtime"), dim("("+url+")"))
	row("uptime", val(humanDuration(time.Duration(rs.UptimeSec*float64(time.Second)))))
	row("goroutines", val(fmt.Sprintf("%d", rs.Goroutines)))
	row("gc cycles", val(fmt.Sprintf("%d", rs.NumGC)))

	// Memory: first / max / current per tracked type, in the server's order.
	fmt.Fprintf(out, "  %s %s\n", label(fmt.Sprintf("%-14s", "memory")),
		dim("first → max → current"))
	for _, name := range memOrder(rs) {
		m := rs.Mem[name]
		row("  "+name, val(fmt.Sprintf("%s → %s → %s",
			humanBytes(int64(m.First)), humanBytes(int64(m.Max)), humanBytes(int64(m.Current)))))
	}

	// Database read latency.
	rd := rs.Reads
	row("db reads", val(fmt.Sprintf("%d queries", rd.Count)))
	row("read latency", val(fmt.Sprintf("avg %.2fms, max %.2fms, last %.2fms", rd.AvgMs, rd.MaxMs, rd.LastMs)))
	fmt.Fprintln(out)
}

// memOrder returns the server-provided ordering, falling back to map iteration
// order only if the server did not send one.
func memOrder(rs *RuntimeStats) []string {
	if len(rs.MemOrder) > 0 {
		return rs.MemOrder
	}
	order := make([]string, 0, len(rs.Mem))
	for k := range rs.Mem {
		order = append(order, k)
	}
	return order
}

// humanDuration formats a duration compactly (e.g. "2d 3h 4m").
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// humanBytes formats a byte count compactly (KiB/MiB/GiB).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
