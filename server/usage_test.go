package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestPrintUsage(t *testing.T) {
	var buf bytes.Buffer
	oldOut := color.Output
	oldNo := color.NoColor
	color.Output = &buf
	color.NoColor = true
	defer func() {
		color.Output = oldOut
		color.NoColor = oldNo
	}()

	printUsage()
	out := buf.String()

	for _, want := range []string{
		"PingMon Server v" + appVersion,
		"USAGE",
		"FLAGS",
		"--token",
		"--name",
		"--debug",
		"EXAMPLES",
		"sudo pingmon",
		"openapi.yml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output missing %q\n---\n%s", want, out)
		}
	}
}
