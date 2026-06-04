package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fatih/color"
)

func TestStatusCommandRegistered(t *testing.T) {
	if _, ok := findCommand("status"); !ok {
		t.Fatalf("status command not registered")
	}
}

func TestStatusEffectiveConfigJSON(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "pingmon.conf")
	// Includes an inline comment (the case that bit the user) and arpscan.
	os.WriteFile(conf, []byte(
		"host=100.64.0.177   # tailscale ip\nport=7777\narpscan=true\n"), 0o600)

	var buf bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rc := runStatusCmd([]string{"--json", "--config", conf, "--datafolder", dir})
	w.Close()
	os.Stdout = old
	buf.ReadFrom(r)

	if rc != 0 {
		t.Fatalf("status rc=%d (config should be valid)", rc)
	}
	var s effectiveStatus
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("decode status json: %v\n%s", err, buf.String())
	}
	if s.Host != "100.64.0.177" {
		t.Fatalf("host not parsed (inline comment?): %q", s.Host)
	}
	if s.Port != 7777 {
		t.Fatalf("port: got %d", s.Port)
	}
	if s.Listen != "http://100.64.0.177:7777" {
		t.Fatalf("listen: got %q", s.Listen)
	}
	if !s.ArpScan {
		t.Fatalf("arpscan should be true")
	}
}

func TestStatusReportsConfigErrors(t *testing.T) {
	old := color.Output
	color.Output = &bytes.Buffer{}
	defer func() { color.Output = old }()

	dir := t.TempDir()
	conf := filepath.Join(dir, "bad.conf")
	os.WriteFile(conf, []byte("port=abc\nbogus=1\n"), 0o600)

	if rc := runStatusCmd([]string{"--config", conf, "--datafolder", dir}); rc != 1 {
		t.Fatalf("status on bad config: expected rc=1, got %d", rc)
	}
}
