package main

import (
	"path/filepath"
	"testing"

	"github.com/fatih/color"
)

func TestResolveDBPath(t *testing.T) {
	// The database is always <datafolder>/pingmon.db.
	if got := resolveDBPath("/var/lib/pingmon"); got != filepath.Join("/var/lib/pingmon", "pingmon.db") {
		t.Errorf("got %q", got)
	}
}

func TestMaskToken(t *testing.T) {
	if got := maskToken(validToken1); got != "3f2504e0…3301" {
		t.Errorf("mask: got %q", got)
	}
	if got := maskToken("short"); got != "…" {
		t.Errorf("short mask: got %q", got)
	}
}

func TestColorizeLogLine(t *testing.T) {
	old := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = old }()

	// With color disabled the content is preserved (tags/keywords unchanged).
	for _, in := range []string{"[PING] hello", "ERROR: boom", "WARNING: hmm", "plain line"} {
		if got := colorizeLogLine(in); got != in {
			t.Errorf("colorizeLogLine(%q) altered content with NoColor: %q", in, got)
		}
	}
}
