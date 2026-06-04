package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/fatih/color"
)

// defaultConfig is the shipped demo config, installed by `config set`.
//
//go:embed etc/pingmon.conf
var defaultConfig string

// defaultConfigPath is where the server looks for its config by default.
const defaultConfigPath = "/etc/pingmon.conf"

func init() {
	registerCommand(command{
		name:    "config",
		summary: "Validate or install the config file: config test|set",
		run:     runConfigCmd,
	})
}

func runConfigCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(color.Error, "usage: pingmon config <test|set> [path] [--yes]")
		return 2
	}
	switch args[0] {
	case "test":
		return configTest(args[1:])
	case "set":
		return configSet(args[1:])
	default:
		fmt.Fprintf(color.Error, "unknown config subcommand: %s\n", args[0])
		return 2
	}
}

// pathFromArgs returns a positional path argument or the default.
func pathFromArgs(fs *flag.FlagSet, def string) string {
	if fs.NArg() > 0 {
		return fs.Arg(0)
	}
	return def
}

func configTest(args []string) int {
	fs := flag.NewFlagSet("config test", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(color.Output, "usage: pingmon config test [path]   (default "+defaultConfigPath+")")
	}
	_ = fs.Parse(args)
	path := pathFromArgs(fs, defaultConfigPath)

	out := color.Output
	ok := color.New(color.FgGreen, color.Bold).SprintFunc()
	bad := color.New(color.FgRed, color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	cfg, found, errs := loadConfig(path)
	if !found {
		fmt.Fprintf(out, "%s no config file at %s (built-in defaults apply)\n", dim("·"), path)
		return 0
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(out, "  %s %v\n", bad("✗"), e)
		}
		fmt.Fprintf(out, "\n%s %s has %d problem(s)\n", bad("FAIL"), path, len(errs))
		return 1
	}
	fmt.Fprintf(out, "%s %s is valid\n", ok("OK"), path)
	fmt.Fprintln(out, dim(summarizeConfig(cfg)))
	return 0
}

// summarizeConfig lists the keys actually set in the file.
func summarizeConfig(c Config) string {
	var set []string
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("host", c.Host != nil)
	add("port", c.Port != nil)
	add("datafolder", c.DataFolder != nil)
	add("db", c.DB != nil)
	add("raw_retain", c.RawRetain != nil)
	add("rollup_interval", c.RollupInterval != nil)
	add("debug", c.Debug != nil)
	add("name", c.Name != nil)
	add("readonly", c.ReadOnly != nil)
	add("arpscan", c.ArpScan != nil)
	add("arp_interval", c.ArpInterval != nil)
	add("minimum_ping_interval", c.MinPingInterval != nil)
	if len(c.Tokens) > 0 {
		set = append(set, fmt.Sprintf("token(%d)", len(c.Tokens)))
	}
	if len(set) == 0 {
		return "  (no settings active; all commented out)"
	}
	out := "  set: "
	for i, s := range set {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func configSet(args []string) int {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	yes := fs.Bool("yes", false, "Skip the overwrite prompt")
	fs.Usage = func() {
		fmt.Fprintln(color.Output, "usage: pingmon config set [path] [--yes]   (default "+defaultConfigPath+")")
	}
	_ = fs.Parse(args)
	path := pathFromArgs(fs, defaultConfigPath)

	out := color.Output
	if _, err := os.Stat(path); err == nil {
		// Exists: ask before overwriting.
		if !*yes && !confirm(fmt.Sprintf("%s already exists. Overwrite with the default config? [y/N] ", path)) {
			fmt.Fprintln(out, "aborted (existing file kept)")
			return 1
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(color.Error, "config set: cannot access %s: %v\n", path, err)
		return 1
	}

	if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
		fmt.Fprintf(color.Error, "config set: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "wrote default config to %s\n", path)
	fmt.Fprintln(out, color.New(color.Faint).Sprint("Edit it, then validate with: pingmon config test"))
	return 0
}
