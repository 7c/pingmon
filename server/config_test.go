package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func errorsJoined(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}

func TestValidateConfigValid(t *testing.T) {
	raw := map[string][]string{
		"host":                  {"0.0.0.0"},
		"port":                  {"8888"},
		"datafolder":            {"/var/lib/pingmon"},
		"db":                    {"pingmon.db"},
		"raw_retain":            {"720h"},
		"rollup_interval":       {"5m"},
		"debug":                 {"true"},
		"name":                  {"edge-1"},
		"readonly":              {"no"},
		"arpscan":               {"on"},
		"arp_interval":          {"30s"},
		"minimum_ping_interval": {"500ms"},
		"token":                 {validToken1 + "," + validToken2},
	}
	c, errs := validateConfigMap(raw)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got:\n%s", errorsJoined(errs))
	}
	if c.Host == nil || *c.Host != "0.0.0.0" || c.Port == nil || *c.Port != 8888 {
		t.Fatalf("host/port not parsed: %+v", c)
	}
	if c.Debug == nil || !*c.Debug || c.ReadOnly == nil || *c.ReadOnly || c.ArpScan == nil || !*c.ArpScan {
		t.Fatalf("bools not parsed: %+v", c)
	}
	if c.MinPingInterval == nil || *c.MinPingInterval != 500*time.Millisecond {
		t.Fatalf("min interval not parsed: %+v", c)
	}
	if len(c.Tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %v", c.Tokens)
	}
}

func TestValidateConfigHumanErrors(t *testing.T) {
	raw := map[string][]string{
		"prot":                  {"8080"},        // typo / unknown key
		"port":                  {"abc"},         // not an int
		"debug":                 {"maybe"},       // not a bool
		"rollup_interval":       {"0s"},          // must be > 0
		"arp_interval":          {"5"},           // missing unit
		"raw_retain":            {"-1h"},         // negative
		"minimum_ping_interval": {"500ms", "1s"}, // set twice
		"token":                 {"NOT-A-UUID"},  // bad token
	}
	_, errs := validateConfigMap(raw)
	joined := errorsJoined(errs)

	for _, want := range []string{
		`unknown setting "prot"`,
		"port:",
		"debug:",
		"rollup_interval:",
		"arp_interval:",
		"raw_retain:",
		"set 2 times",
		"not a lowercase uuid4",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing error %q in:\n%s", want, joined)
		}
	}
	// Strict but graceful: all problems reported at once.
	if len(errs) < 7 {
		t.Errorf("expected several errors, got %d:\n%s", len(errs), joined)
	}
}

func TestPortRange(t *testing.T) {
	if _, errs := validateConfigMap(map[string][]string{"port": {"70000"}}); len(errs) == 0 {
		t.Fatalf("expected out-of-range port error")
	}
	if _, errs := validateConfigMap(map[string][]string{"port": {"0"}}); len(errs) == 0 {
		t.Fatalf("expected out-of-range port error for 0")
	}
}

func TestLoadConfigMissingAndValid(t *testing.T) {
	dir := t.TempDir()

	// Missing file.
	_, found, errs := loadConfig(filepath.Join(dir, "nope.conf"))
	if found || len(errs) != 0 {
		t.Fatalf("missing file: found=%v errs=%v", found, errs)
	}

	// Valid file.
	path := filepath.Join(dir, "pingmon.conf")
	os.WriteFile(path, []byte("port=9000\ndebug=true\n# comment\n"), 0o600)
	c, found, errs := loadConfig(path)
	if !found || len(errs) != 0 {
		t.Fatalf("valid file: found=%v errs=%v", found, errorsJoined(errs))
	}
	if c.Port == nil || *c.Port != 9000 {
		t.Fatalf("port not loaded: %+v", c)
	}
}

func TestConfigSetAndTestCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pingmon.conf")

	// set (file does not exist yet → installs default without prompting).
	if rc := configSet([]string{path}); rc != 0 {
		t.Fatalf("config set rc=%d", rc)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if string(data) != defaultConfig {
		t.Fatalf("written config does not match embedded default")
	}

	// test the freshly-installed default (all commented → valid, no settings).
	if rc := configTest([]string{path}); rc != 0 {
		t.Fatalf("config test on default rc=%d", rc)
	}

	// overwrite with --yes.
	if rc := configSet([]string{"--yes", path}); rc != 0 {
		t.Fatalf("config set overwrite rc=%d", rc)
	}

	// test a broken file → rc 1.
	bad := filepath.Join(dir, "bad.conf")
	os.WriteFile(bad, []byte("port=abc\nbogus=1\n"), 0o600)
	if rc := configTest([]string{bad}); rc != 1 {
		t.Fatalf("config test on bad file: expected rc=1, got %d", rc)
	}

	// test a missing file → rc 0 (defaults apply).
	if rc := configTest([]string{filepath.Join(dir, "absent.conf")}); rc != 0 {
		t.Fatalf("config test on missing file: expected rc=0, got %d", rc)
	}
}

func TestConfigCommandRegistered(t *testing.T) {
	if _, ok := findCommand("config"); !ok {
		t.Fatalf("config command not registered")
	}
}
