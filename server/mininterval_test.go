package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/7c/pingmon/classes/store"
)

func TestClampConfig(t *testing.T) {
	prev := minPingIntervalMs
	defer func() { minPingIntervalMs = prev }()
	minPingIntervalMs = 500

	// Below the floor is raised.
	got := clampConfig(store.HostConfig{IntervalMs: 100, TimeoutMs: 2000, PacketSize: 56})
	if got.IntervalMs != 500 {
		t.Errorf("expected clamp to 500, got %d", got.IntervalMs)
	}
	// Zero/negative also clamped.
	if clampConfig(store.HostConfig{IntervalMs: 0}).IntervalMs != 500 {
		t.Errorf("zero should clamp to 500")
	}
	// At/above the floor is unchanged.
	if got := clampConfig(store.HostConfig{IntervalMs: 1000}); got.IntervalMs != 1000 {
		t.Errorf("expected 1000 unchanged, got %d", got.IntervalMs)
	}
}

func TestOptionsFromConfigEnforcesMinimum(t *testing.T) {
	prev := minPingIntervalMs
	defer func() { minPingIntervalMs = prev }()
	minPingIntervalMs = 500

	opts := optionsFromConfig(store.HostConfig{IntervalMs: 50, TimeoutMs: 2000, PacketSize: 56})
	if opts.Interval.Milliseconds() != 500 {
		t.Fatalf("expected pinger interval >= 500ms, got %v", opts.Interval)
	}
}

// TestCreateHostClampsInterval drives the create handler and confirms the
// persisted config reflects the enforced minimum (so the UI sees the real value).
func TestCreateHostClampsInterval(t *testing.T) {
	prev := minPingIntervalMs
	defer func() { minPingIntervalMs = prev }()
	minPingIntervalMs = 500

	r, pm := newTestServer(t)
	body := map[string]interface{}{
		"ip":     testIP,
		"config": map[string]int{"intervalMs": 100, "timeoutMs": 2000, "packetSize": 56},
	}
	rec := do(t, r, http.MethodPost, "/api/hosts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	h, err := pm.store.GetHost(testIP)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if h.Config.IntervalMs != 500 {
		t.Fatalf("expected stored interval clamped to 500, got %d", h.Config.IntervalMs)
	}

	// Update below the floor is also clamped.
	rec = do(t, r, http.MethodPut, "/api/hosts/"+testIP+"/config",
		map[string]int{"intervalMs": 200, "timeoutMs": 2000, "packetSize": 56})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d", rec.Code)
	}
	h, _ = pm.store.GetHost(testIP)
	if h.Config.IntervalMs != 500 {
		t.Fatalf("expected updated interval clamped to 500, got %d", h.Config.IntervalMs)
	}
}

// TestProfileReportsMinInterval confirms the floor is advertised.
func TestProfileReportsMinInterval(t *testing.T) {
	r, _ := newTestServer(t)
	rec := do(t, r, http.MethodGet, "/api/profile", nil)
	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["minIntervalMs"]; !ok {
		t.Fatalf("profile missing minIntervalMs: %s", rec.Body.String())
	}
}
