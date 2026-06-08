package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestRuntimeEndpoint(t *testing.T) {
	r, _ := newTestServer(t)

	rec := do(t, r, http.MethodGet, "/api/runtime", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/runtime: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var rs RuntimeStats
	if err := json.Unmarshal(rec.Body.Bytes(), &rs); err != nil {
		t.Fatalf("decode runtime stats: %v", err)
	}
	if len(rs.Mem) == 0 {
		t.Fatal("runtime stats missing memory metrics")
	}
	if _, ok := rs.Mem["alloc"]; !ok {
		t.Fatal("runtime stats missing 'alloc' metric")
	}
	if rs.Goroutines <= 0 {
		t.Fatalf("goroutines = %d, want > 0", rs.Goroutines)
	}
	if len(rs.MemOrder) == 0 {
		t.Fatal("runtime stats missing memOrder")
	}
}

// TestRuntimeRequiresAuth locks in the decision that /api/runtime is NOT public:
// it must be gated behind token auth like the rest of the API surface.
func TestRuntimeRequiresAuth(t *testing.T) {
	if !requiresAuth("/api/runtime") {
		t.Fatal("/api/runtime must require auth (not be in publicAPIPaths)")
	}
}

// TestProbeRuntime drives the `pingmon stats` client path against a live server.
func TestProbeRuntime(t *testing.T) {
	r, _ := newTestServer(t)
	ts := httptest.NewServer(r)
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test url: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	cfg := Config{Host: &host, Port: &port}

	rs, gotURL, ok := probeRuntime(cfg)
	if !ok {
		t.Fatalf("probeRuntime: expected reachable server at %s", ts.URL)
	}
	if rs == nil || len(rs.Mem) == 0 {
		t.Fatal("probeRuntime returned no memory metrics")
	}
	if gotURL == "" {
		t.Fatal("probeRuntime returned empty URL")
	}
}

func TestProbeRuntimeNotRunning(t *testing.T) {
	host := "127.0.0.1"
	port := 1 // nothing listens here; connection is refused quickly
	cfg := Config{Host: &host, Port: &port}

	if _, _, ok := probeRuntime(cfg); ok {
		t.Fatal("probeRuntime should report not-running when nothing listens")
	}
}

func TestDialHost(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0":     "127.0.0.1",
		"::":          "127.0.0.1",
		"eth0":        "127.0.0.1",
		"10.0.0.5":    "10.0.0.5",
		"192.168.1.1": "192.168.1.1",
	}
	for in, want := range cases {
		if got := dialHost(in); got != want {
			t.Errorf("dialHost(%q) = %q, want %q", in, got, want)
		}
	}
}
