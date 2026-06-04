package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestBuildUnit(t *testing.T) {
	unit := buildUnit("/opt/pingmon/bin/pingmon", "/opt/pingmon")
	for _, want := range []string{
		"Description=PingMon",
		"ExecStart=/opt/pingmon/bin/pingmon",
		"WorkingDirectory=/opt/pingmon",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q\n---\n%s", want, unit)
		}
	}
}

func TestSystemctlCommandRegistered(t *testing.T) {
	if _, ok := findCommand("systemctl"); !ok {
		t.Fatalf("systemctl command not registered")
	}
}

func TestSystemctlRequiresSystemd(t *testing.T) {
	err := requireSystemd()
	if runtime.GOOS == "linux" {
		// On Linux it depends on systemctl being present; don't assert.
		return
	}
	if err == nil {
		t.Fatalf("expected requireSystemd to fail on %s", runtime.GOOS)
	}
	// The subcommands should fail fast (rc=1) without prompting on non-Linux.
	if rc := runSystemctlCmd([]string{"enable"}); rc != 1 {
		t.Fatalf("expected rc=1 on non-systemd platform, got %d", rc)
	}
}

func TestSystemctlNoSubcommand(t *testing.T) {
	if rc := runSystemctlCmd(nil); rc != 2 {
		t.Fatalf("expected rc=2 for missing subcommand, got %d", rc)
	}
	if rc := runSystemctlCmd([]string{"bogus"}); rc != 2 {
		t.Fatalf("expected rc=2 for unknown subcommand, got %d", rc)
	}
}
