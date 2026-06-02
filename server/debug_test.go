package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestDebugfRespectsFlag(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	prev := debugEnabled
	defer func() { debugEnabled = prev }()

	debugEnabled = false
	debugf("should-not-appear %d", 1)
	if buf.Len() != 0 {
		t.Fatalf("expected no output when debug disabled, got %q", buf.String())
	}

	debugEnabled = true
	debugf("marker-%d", 99)
	out := buf.String()
	if !strings.Contains(out, "[DEBUG] marker-99") {
		t.Fatalf("expected [DEBUG] line when enabled, got %q", out)
	}
}
