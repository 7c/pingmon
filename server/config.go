package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// parseKVFile reads a KEY=VALUE config file into a map of lowercased key ->
// values. Keys may repeat (each occurrence is appended). A missing file is not
// an error (returns nil, nil). Surrounding quotes on values are stripped.
func parseKVFile(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open config file %q: %w", path, err)
	}
	defer f.Close()

	out := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		out[key] = append(out[key], parseConfigValue(v))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}
	return out, nil
}

// parseConfigValue extracts the value from the right-hand side of KEY=VALUE,
// supporting surrounding quotes and trailing inline comments (e.g.
// `host=1.2.3.4   # bind address` -> "1.2.3.4"). A '#' is only treated as a
// comment when preceded by whitespace, so unquoted values containing '#' (like
// `foo#bar`) are preserved. Quoted values may contain anything, including '#'.
func parseConfigValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(v[1:], q); i >= 0 {
			return v[1 : 1+i] // content between the quotes (comment after is ignored)
		}
		return strings.TrimSpace(strings.Trim(v, string(q))) // unterminated quote
	}
	if v[0] == '#' {
		return "" // whole value is a comment
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// Config holds settings loaded from /etc/pingmon.conf. Pointer fields are nil
// when the key was not present, so callers can tell "unset" from "set to zero".
type Config struct {
	Host            *string
	Port            *int
	DataFolder      *string
	DB              *string
	RawRetain       *time.Duration
	RollupInterval  *time.Duration
	Debug           *bool
	Name            *string
	ReadOnly        *bool
	ArpScan         *bool
	ArpInterval     *time.Duration
	MinPingInterval *time.Duration
	Tokens          []string
}

// keyKind classifies how a config value is parsed.
type keyKind int

const (
	kString keyKind = iota
	kInt
	kBool
	kDuration    // >= 0
	kDurationPos // > 0
	kTokens
)

// configKeys maps every accepted setting to its kind. Keys mirror the config keys
// and the command-line flags.
var configKeys = map[string]keyKind{
	"host":                  kString,
	"port":                  kInt,
	"datafolder":            kString,
	"db":                    kString,
	"raw_retain":            kDuration,
	"rollup_interval":       kDurationPos,
	"debug":                 kBool,
	"name":                  kString,
	"readonly":              kBool,
	"arpscan":               kBool,
	"arp_interval":          kDurationPos,
	"minimum_ping_interval": kDurationPos,
	"token":                 kTokens,
}

// loadConfig reads and validates a config file. It returns the parsed Config,
// whether the file exists, and any human-readable validation errors (sorted).
// A missing file is not an error (found=false, no errors).
func loadConfig(path string) (Config, bool, []error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, []error{fmt.Errorf("cannot access %s: %v", path, err)}
	}
	raw, err := parseKVFile(path)
	if err != nil {
		return Config{}, true, []error{err}
	}
	cfg, errs := validateConfigMap(raw)
	return cfg, true, errs
}

// validateConfigMap validates a parsed key→values map strictly, collecting all
// problems rather than failing on the first.
func validateConfigMap(raw map[string][]string) (Config, []error) {
	var c Config
	var errs []error
	add := func(format string, args ...interface{}) { errs = append(errs, fmt.Errorf(format, args...)) }

	for key, vals := range raw {
		kind, ok := configKeys[key]
		if !ok {
			add("unknown setting %q", key)
			continue
		}
		if kind != kTokens && len(vals) > 1 {
			add("%q is set %d times (only one value allowed)", key, len(vals))
			continue
		}

		// Last value wins for single-valued keys.
		val := ""
		if len(vals) > 0 {
			val = vals[len(vals)-1]
		}

		switch key {
		case "host":
			s := val
			c.Host = &s
		case "name":
			s := val
			c.Name = &s
		case "datafolder":
			s := val
			c.DataFolder = &s
		case "db":
			s := val
			c.DB = &s

		case "port":
			n, err := strconv.Atoi(val)
			if err != nil {
				add("port: %q is not an integer", val)
			} else if n < 1 || n > 65535 {
				add("port: %d is out of range (1..65535)", n)
			} else {
				c.Port = &n
			}

		case "debug":
			if b, err := parseBoolStrict(val); err != nil {
				add("debug: %v", err)
			} else {
				c.Debug = &b
			}
		case "readonly":
			if b, err := parseBoolStrict(val); err != nil {
				add("readonly: %v", err)
			} else {
				c.ReadOnly = &b
			}
		case "arpscan":
			if b, err := parseBoolStrict(val); err != nil {
				add("arpscan: %v", err)
			} else {
				c.ArpScan = &b
			}

		case "raw_retain":
			if d, err := parseDurStrict(val, false); err != nil {
				add("raw_retain: %v", err)
			} else {
				c.RawRetain = &d
			}
		case "rollup_interval":
			if d, err := parseDurStrict(val, true); err != nil {
				add("rollup_interval: %v", err)
			} else {
				c.RollupInterval = &d
			}
		case "arp_interval":
			if d, err := parseDurStrict(val, true); err != nil {
				add("arp_interval: %v", err)
			} else {
				c.ArpInterval = &d
			}
		case "minimum_ping_interval":
			if d, err := parseDurStrict(val, true); err != nil {
				add("minimum_ping_interval: %v", err)
			} else {
				c.MinPingInterval = &d
			}

		case "token":
			for _, rawVal := range vals {
				for _, t := range strings.Split(rawVal, ",") {
					t = strings.TrimSpace(t)
					if t == "" {
						continue
					}
					if !isValidUUID4Lower(t) {
						add("token %q is not a lowercase uuid4", t)
						continue
					}
					c.Tokens = append(c.Tokens, t)
				}
			}
		}
	}

	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return c, errs
}

// parseBoolStrict accepts true/false/1/0/yes/no/on/off (case-insensitive).
func parseBoolStrict(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%q is not a boolean (use true/false)", s)
	}
}

// parseDurStrict parses a Go duration; mustPositive requires > 0.
func parseDurStrict(s string, mustPositive bool) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (e.g. 500ms, 30s, 5m, 1h)", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q must not be negative", s)
	}
	if mustPositive && d == 0 {
		return 0, fmt.Errorf("%q must be greater than zero", s)
	}
	return d, nil
}

// applyConfigToFlags applies config values to flag variables, but only for flags
// the user did not set explicitly on the command line (flag > config > default).
// Tokens are handled separately (merged additively).
func applyConfigToFlags(c Config, setFlags map[string]bool) {
	apply := func(flagName string, set func()) {
		if !setFlags[flagName] {
			set()
		}
	}
	if c.Host != nil {
		apply("host", func() { *hostFlag = *c.Host })
	}
	if c.Port != nil {
		apply("port", func() { *portFlag = *c.Port })
	}
	if c.DataFolder != nil {
		apply("datafolder", func() { *dataFolderFlag = *c.DataFolder })
	}
	if c.DB != nil {
		apply("db", func() { *dbFlag = *c.DB })
	}
	if c.RawRetain != nil {
		apply("raw-retain", func() { *rawRetainFlag = *c.RawRetain })
	}
	if c.RollupInterval != nil {
		apply("rollup-interval", func() { *rollupFlag = *c.RollupInterval })
	}
	if c.Debug != nil {
		apply("debug", func() { *debugFlag = *c.Debug })
	}
	if c.Name != nil {
		apply("name", func() { *nameFlag = *c.Name })
	}
	if c.ReadOnly != nil {
		apply("readonly", func() { *readonlyFlag = *c.ReadOnly })
	}
	if c.ArpScan != nil {
		apply("arpscan", func() { *arpscanFlag = *c.ArpScan })
	}
	if c.ArpInterval != nil {
		apply("arp-interval", func() { *arpIntervalF = *c.ArpInterval })
	}
	if c.MinPingInterval != nil {
		apply("min-ping-interval", func() { *minPingFlag = *c.MinPingInterval })
	}
}
