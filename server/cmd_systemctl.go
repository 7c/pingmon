package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fatih/color"
)

const (
	serviceName = "pingmon"
	unitPath    = "/etc/systemd/system/pingmon.service"
)

func init() {
	registerCommand(command{
		name:    "systemctl",
		summary: "Manage the systemd service: systemctl install|uninstall|enable|disable",
		run:     runSystemctlCmd,
	})
}

func runSystemctlCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(color.Error, "usage: pingmon systemctl <install|uninstall|enable|disable> [--yes]")
		return 2
	}
	switch args[0] {
	case "install":
		return systemctlInstall(args[1:])
	case "uninstall":
		return systemctlUninstall(args[1:])
	case "enable":
		return systemctlSimple(args[1:], "enable", "--now")
	case "disable":
		return systemctlSimple(args[1:], "disable", "--now")
	default:
		fmt.Fprintf(color.Error, "unknown systemctl subcommand: %s\n", args[0])
		return 2
	}
}

// requireSystemd verifies we are on Linux with systemctl available.
func requireSystemd() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("systemd integration is only available on Linux")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found on PATH")
	}
	return nil
}

// preflight checks systemd availability and root privileges.
func preflight() error {
	if err := requireSystemd(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command must be run as root (try: sudo pingmon systemctl ...)")
	}
	return nil
}

// buildUnit renders the systemd unit for the given binary + working directory.
func buildUnit(exe, workdir string) string {
	return fmt.Sprintf(`[Unit]
Description=PingMon ping monitoring server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, exe, workdir)
}

// detectPaths returns the absolute binary path and working directory used for
// the unit (auto-detected).
func detectPaths() (exe, workdir string) {
	exe, err := os.Executable()
	if err == nil {
		if resolved, err2 := filepath.EvalSymlinks(exe); err2 == nil {
			exe = resolved
		}
	}
	workdir, _ = os.Getwd()
	return exe, workdir
}

func systemctlInstall(args []string) int {
	fs := flag.NewFlagSet("systemctl install", flag.ExitOnError)
	yes := fs.Bool("yes", false, "Skip the confirmation prompt")
	_ = fs.Parse(args)

	if err := preflight(); err != nil {
		fmt.Fprintf(color.Error, "systemctl install: %v\n", err)
		return 1
	}

	exe, workdir := detectPaths()
	unit := buildUnit(exe, workdir)

	out := color.Output
	title := color.New(color.FgCyan, color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()
	fmt.Fprintf(out, "%s\n", title("Install PingMon as a systemd service"))
	fmt.Fprintf(out, "  unit file:        %s\n", unitPath)
	fmt.Fprintf(out, "  ExecStart:        %s\n", exe)
	fmt.Fprintf(out, "  WorkingDirectory: %s\n", workdir)
	fmt.Fprintf(out, "  on success:       %s\n\n", dim("systemctl enable --now "+serviceName))
	fmt.Fprintln(out, dim(strings.TrimRight(unit, "\n")))
	fmt.Fprintln(out)

	if !*yes && !confirm("Write this unit, then enable and start the service? [y/N] ") {
		fmt.Fprintln(out, "aborted")
		return 1
	}

	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		fmt.Fprintf(color.Error, "systemctl install: write unit: %v\n", err)
		return 1
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return 1
	}
	if err := runSystemctl("enable", "--now", serviceName); err != nil {
		return 1
	}
	fmt.Fprintf(out, "\n%s installed and started. Check it with: systemctl status %s\n", serviceName, serviceName)
	return 0
}

func systemctlUninstall(args []string) int {
	fs := flag.NewFlagSet("systemctl uninstall", flag.ExitOnError)
	yes := fs.Bool("yes", false, "Skip the confirmation prompt")
	_ = fs.Parse(args)

	if err := preflight(); err != nil {
		fmt.Fprintf(color.Error, "systemctl uninstall: %v\n", err)
		return 1
	}

	out := color.Output
	fmt.Fprintf(out, "This will stop, disable, and remove the %s service (%s).\n", serviceName, unitPath)
	if !*yes && !confirm("Proceed? [y/N] ") {
		fmt.Fprintln(out, "aborted")
		return 1
	}

	// Best-effort stop+disable; ignore errors (may already be stopped/absent).
	_ = runSystemctlQuiet("disable", "--now", serviceName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(color.Error, "systemctl uninstall: remove unit: %v\n", err)
		return 1
	}
	_ = runSystemctlQuiet("daemon-reload")
	fmt.Fprintf(out, "%s uninstalled.\n", serviceName)
	return 0
}

// systemctlSimple runs `systemctl <verb> [extra...] pingmon` (enable/disable).
func systemctlSimple(args []string, verb string, extra ...string) int {
	fs := flag.NewFlagSet("systemctl "+verb, flag.ExitOnError)
	_ = fs.Parse(args)

	if err := preflight(); err != nil {
		fmt.Fprintf(color.Error, "systemctl %s: %v\n", verb, err)
		return 1
	}
	if err := runSystemctl(append(append([]string{verb}, extra...), serviceName)...); err != nil {
		return 1
	}
	fmt.Fprintf(color.Output, "%s %sd.\n", serviceName, verb)
	return 0
}

// runSystemctl runs systemctl, streaming output, and reports failures.
func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = color.Output
	cmd.Stderr = color.Error
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(color.Error, "systemctl %s failed: %v\n", strings.Join(args, " "), err)
		return err
	}
	return nil
}

// runSystemctlQuiet runs systemctl, discarding output and returning the error.
func runSystemctlQuiet(args ...string) error {
	return exec.Command("systemctl", args...).Run()
}

// confirm prompts on stdin and returns true for an affirmative answer.
func confirm(prompt string) bool {
	fmt.Fprint(color.Output, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
