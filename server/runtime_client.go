package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// runtimeProbeTimeout bounds how long `pingmon stats` waits for a running server
// before falling back to a stored-state-only view.
const runtimeProbeTimeout = 1500 * time.Millisecond

// probeRuntime tries to fetch live runtime stats from a running server described
// by the given config. It returns the stats and the URL queried, or ok=false if
// no server is reachable (which is normal — the server may simply be stopped).
func probeRuntime(cfg Config) (*RuntimeStats, string, bool) {
	host := dialHost(deref(cfg.Host, "127.0.0.1"))
	port := derefInt(cfg.Port, 6868)
	url := fmt.Sprintf("http://%s/api/runtime", net.JoinHostPort(host, strconv.Itoa(port)))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, url, false
	}
	// The endpoint is auth-gated; present a configured token if one exists.
	if len(cfg.Tokens) > 0 {
		req.Header.Set("Authorization", "Bearer "+cfg.Tokens[0])
	}

	client := &http.Client{Timeout: runtimeProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, url, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, url, false
	}

	var rs RuntimeStats
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		return nil, url, false
	}
	return &rs, url, true
}

// dialHost maps a listen host to an address usable for a local probe. A wildcard
// bind (0.0.0.0 / ::) is not itself connectable, so we target loopback instead;
// an interface name is likewise not dialable, so loopback is the safe default.
func dialHost(h string) string {
	switch h {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	}
	if net.ParseIP(h) != nil {
		return h
	}
	// Interface name or comma-list: a running server reachable from this host is
	// reachable on loopback as well in the common single-box case.
	return "127.0.0.1"
}
