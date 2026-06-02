package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// parseEnvFile reads a .env-style file into a map of lowercased key -> values.
// Keys may repeat (each occurrence is appended). A missing file is not an error
// (returns nil, nil). Surrounding quotes on values are stripped.
func parseEnvFile(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open env file %q: %w", path, err)
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
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		out[key] = append(out[key], strings.TrimSpace(val))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read env file %q: %w", path, err)
	}
	return out, nil
}

// envDuration returns the last value of key parsed as a Go duration, or def when
// absent. An unparseable value logs a warning and falls back to def.
func envDuration(env map[string][]string, key string, def time.Duration) time.Duration {
	vals := env[key]
	if len(vals) == 0 {
		return def
	}
	d, err := time.ParseDuration(vals[len(vals)-1])
	if err != nil {
		log.Printf("WARNING: invalid %s in env file (%q): %v; using %s", key, vals[len(vals)-1], err, def)
		return def
	}
	return d
}
