package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"

	"github.com/7c/pingmon/classes/store"
)

func init() {
	registerCommand(command{
		name:    "status",
		summary: "Show the effective configuration and a quick snapshot (without starting the server)",
		run:     runStatusCmd,
	})
}

// effectiveStatus is the resolved view shown by `pingmon status`.
type effectiveStatus struct {
	Version        string       `json:"version"`
	Name           string       `json:"name"`
	ConfigPath     string       `json:"configPath"`
	ConfigFound    bool         `json:"configFound"`
	ConfigErrors   []string     `json:"configErrors,omitempty"`
	Host           string       `json:"host"`
	Port           int          `json:"port"`
	Listen         string       `json:"listen"`
	DataFolder     string       `json:"dataFolder"`
	Database       string       `json:"database"`
	DatabaseExists bool         `json:"databaseExists"`
	DatabaseBytes  int64        `json:"databaseBytes"`
	StoreStats     *store.Stats `json:"store,omitempty"`
	MinIntervalMs  int          `json:"minIntervalMs"`
	RawRetain      string       `json:"rawRetain"`
	RollupInterval string       `json:"rollupInterval"`
	ArpScan        bool         `json:"arpScan"`
	ArpInterval    string       `json:"arpInterval"`
	ReadOnly       bool         `json:"readOnly"`
	Debug          bool         `json:"debug"`
	AuthEnabled    bool         `json:"authEnabled"`
	Tokens         []string     `json:"-"`
}

func runStatusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configP := fs.String("config", defaultConfigPath, "Config file to read")
	dfFlag := fs.String("datafolder", "", "Override data folder")
	jsonOut := fs.Bool("json", false, "Output JSON")
	_ = fs.Parse(args)

	cfg, found, errs := loadConfig(*configP)

	// Resolve effective values: command-line override > config > default.
	dataFolder := firstNonEmpty(*dfFlag, deref(cfg.DataFolder, ""), defaultDataFolder)
	absFolder, _ := filepath.Abs(dataFolder)
	dbPath := resolveDBPath(absFolder)

	minMs := 500
	if cfg.MinPingInterval != nil {
		if ms := int(*cfg.MinPingInterval / time.Millisecond); ms > 0 {
			minMs = ms
		}
	}

	st := effectiveStatus{
		Version:        appVersion,
		Name:           deref(cfg.Name, defaultServerName()),
		ConfigPath:     *configP,
		ConfigFound:    found,
		Host:           deref(cfg.Host, "127.0.0.1"),
		Port:           derefInt(cfg.Port, 6868),
		DataFolder:     absFolder,
		Database:       dbPath,
		DatabaseExists: fileExists(dbPath),
		MinIntervalMs:  minMs,
		RawRetain:      durStr(derefDur(cfg.RawRetain, 720*time.Hour)),
		RollupInterval: durStr(derefDur(cfg.RollupInterval, 5*time.Minute)),
		ArpScan:        derefBool(cfg.ArpScan, false),
		ArpInterval:    durStr(derefDur(cfg.ArpInterval, 2*time.Minute)),
		ReadOnly:       derefBool(cfg.ReadOnly, false),
		Debug:          derefBool(cfg.Debug, false),
		AuthEnabled:    len(cfg.Tokens) > 0,
		Tokens:         cfg.Tokens,
	}
	st.Listen = fmt.Sprintf("http://%s:%d", st.Host, st.Port)
	for _, e := range errs {
		st.ConfigErrors = append(st.ConfigErrors, e.Error())
	}

	// Read store stats if the database already exists (never create it here).
	if st.DatabaseExists {
		if fi, err := os.Stat(dbPath); err == nil {
			st.DatabaseBytes = fi.Size()
		}
		if s, err := store.New(dbPath); err == nil {
			if stats, err := s.Stats(); err == nil {
				st.StoreStats = &stats
			}
			s.Close()
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(st)
		return statusExit(errs)
	}
	printStatus(st)
	return statusExit(errs)
}

// statusExit returns 1 when the config has problems, else 0.
func statusExit(errs []error) int {
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func printStatus(s effectiveStatus) {
	out := color.Output
	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	label := color.New(color.Faint).SprintFunc()
	val := color.New(color.FgWhite, color.Bold).SprintFunc()
	on := color.New(color.FgGreen, color.Bold).SprintFunc()
	off := color.New(color.Faint).SprintFunc()
	bad := color.New(color.FgRed, color.Bold).SprintFunc()
	warn := color.New(color.FgYellow, color.Bold).SprintFunc()

	row := func(k, v string) { fmt.Fprintf(out, "  %s %s\n", label(fmt.Sprintf("%-13s", k)), v) }

	fmt.Fprintf(out, "\n%s %s   %s\n", title("● PingMon"), label("v"+s.Version), off("(not running — static view)"))
	row("name", val(s.Name))

	// Config + validation.
	if !s.ConfigFound {
		row("config", off(s.ConfigPath+" (not found; defaults apply)"))
	} else if len(s.ConfigErrors) > 0 {
		row("config", bad(fmt.Sprintf("%s — %d problem(s)", s.ConfigPath, len(s.ConfigErrors))))
		for _, e := range s.ConfigErrors {
			fmt.Fprintf(out, "      %s %s\n", bad("✗"), e)
		}
	} else {
		row("config", val(s.ConfigPath)+" "+on("OK"))
	}

	row("listen", val(s.Listen))
	row("data", val(s.DataFolder))
	if s.DatabaseExists {
		row("database", val(s.Database)+" "+off("("+humanBytes(s.DatabaseBytes)+")"))
	} else {
		row("database", val(s.Database)+" "+off("(not created yet)"))
	}
	if s.StoreStats != nil {
		row("hosts", val(fmt.Sprintf("%d monitored", s.StoreStats.Hosts)))
		row("stored", val(fmt.Sprintf("%d groups, %d annotations, %d results",
			s.StoreStats.Groups, s.StoreStats.Annotations, s.StoreStats.Results)))
	} else {
		row("hosts", off("0 (no database)"))
	}

	row("min interval", val(fmt.Sprintf("%dms", s.MinIntervalMs)))
	row("retention", val(fmt.Sprintf("raw %s, rollup every %s", s.RawRetain, s.RollupInterval)))

	if s.AuthEnabled {
		masked := make([]string, len(s.Tokens))
		for i, t := range s.Tokens {
			masked[i] = maskToken(t)
		}
		row("auth", on(fmt.Sprintf("%d token(s) ", len(s.Tokens)))+label(joinComma(masked)))
	} else {
		row("auth", off("disabled — API is open"))
	}
	row("cors", val("fully open (any origin)"))

	if s.ArpScan {
		row("arpscan", on("on (every "+s.ArpInterval+")"))
	} else {
		row("arpscan", off("off"))
	}
	if s.ReadOnly {
		row("readonly", warn("ON (writes return 423)"))
	} else {
		row("readonly", off("off"))
	}
	if s.Debug {
		row("debug", warn("ON"))
	} else {
		row("debug", off("off"))
	}
	fmt.Fprintln(out)
}

// --- small helpers ---

func deref(p *string, d string) string {
	if p != nil {
		return *p
	}
	return d
}
func derefInt(p *int, d int) int {
	if p != nil {
		return *p
	}
	return d
}
func derefBool(p *bool, d bool) bool {
	if p != nil {
		return *p
	}
	return d
}
func derefDur(p *time.Duration, d time.Duration) time.Duration {
	if p != nil {
		return *p
	}
	return d
}
func durStr(d time.Duration) string { return d.String() }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinComma(xs []string) string {
	out := ""
	for i, s := range xs {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
