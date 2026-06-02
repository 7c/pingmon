package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

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
	df, db := addStoreFlags(fs)
	jsonOut := fs.Bool("json", false, "Output JSON")
	_ = fs.Parse(args)

	st, dbPath, err := openStoreFromFlags(*df, *db)
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

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]interface{}{
			"database":  dbPath,
			"sizeBytes": sizeBytes,
			"stats":     stats,
		})
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
	return 0
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
