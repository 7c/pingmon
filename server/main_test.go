package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/7c/pingmon/classes/store"
)

// testIP resolves without network access and is safe to "ping" in tests (the
// goroutine simply fails with a permission error when not run as root).
const testIP = "127.0.0.1"

// newTestServer builds a router backed by a temp-dir store and returns it along
// with the manager. Pingers and the store are torn down automatically.
func newTestServer(t *testing.T) (*mux.Router, *PingerManager) {
	t.Helper()

	st, err := store.New(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.New() error: %v", err)
	}

	pm := NewPingerManager(st)
	t.Cleanup(func() {
		pm.StopAll()
		st.Close()
	})

	r := mux.NewRouter()
	registerAPIRoutes(r, pm)
	return r, pm
}

// do performs a request against the router and returns the recorder.
func do(t *testing.T, r *mux.Router, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decode unmarshals a JSON response body into a generic map.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestCreateListDeleteHost(t *testing.T) {
	r, _ := newTestServer(t)

	// Create.
	rec := do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ips": []string{testIP}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec)["success"]; got != true {
		t.Fatalf("create: success != true, got %v", got)
	}

	// Read.
	rec = do(t, r, http.MethodGet, "/api/hosts", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	ips := decode(t, rec)["ips"].([]interface{})
	if len(ips) != 1 || ips[0] != testIP {
		t.Fatalf("list: expected [%s], got %v", testIP, ips)
	}

	// Delete.
	rec = do(t, r, http.MethodDelete, "/api/hosts/"+testIP, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", rec.Code)
	}

	// Read again: empty.
	rec = do(t, r, http.MethodGet, "/api/hosts", nil)
	ips = decode(t, rec)["ips"].([]interface{})
	if len(ips) != 0 {
		t.Fatalf("after delete: expected no hosts, got %v", ips)
	}
}

func TestCreateHostSingleIPField(t *testing.T) {
	r, _ := newTestServer(t)

	rec := do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = do(t, r, http.MethodGet, "/api/hosts", nil)
	ips := decode(t, rec)["ips"].([]interface{})
	if len(ips) != 1 || ips[0] != testIP {
		t.Fatalf("expected [%s], got %v", testIP, ips)
	}
}

func TestCreateHostNoIP(t *testing.T) {
	r, _ := newTestServer(t)

	rec := do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if decode(t, rec)["success"] != false {
		t.Fatalf("expected success=false")
	}
}

func TestCreateHostInvalidIP(t *testing.T) {
	r, _ := newTestServer(t)

	// A name that cannot be resolved should surface a per-ip error.
	rec := do(t, r, http.MethodPost, "/api/hosts",
		map[string]interface{}{"ips": []string{"this.is.not.a.valid.host.invalid"}})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("want 206, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["success"] != false {
		t.Fatalf("expected success=false, got %v", body["success"])
	}
	if _, ok := body["errors"]; !ok {
		t.Fatalf("expected errors map, got %v", body)
	}
}

func TestGetStats(t *testing.T) {
	r, _ := newTestServer(t)

	rec := do(t, r, http.MethodGet, "/api/pinger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	// Empty manager returns a JSON object (possibly empty).
	var stats map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("stats not a JSON object: %v", err)
	}

	// After adding a host, stats should include it.
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})
	rec = do(t, r, http.MethodGet, "/api/pinger", nil)
	stats = map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &stats)
	if _, ok := stats[testIP]; !ok {
		t.Fatalf("expected stats for %s, got %v", testIP, stats)
	}
}

func TestResetHost(t *testing.T) {
	r, _ := newTestServer(t)

	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	rec := do(t, r, http.MethodPost, "/api/hosts/"+testIP+"/reset", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: want 200, got %d", rec.Code)
	}
	if decode(t, rec)["success"] != true {
		t.Fatalf("reset: expected success=true")
	}
}

func TestGetHistory(t *testing.T) {
	r, pm := newTestServer(t)

	// Persist a couple of results directly through the store so the test does
	// not depend on ICMP privileges or timing.
	now := time.Now()
	pm.store.SaveResult(store.Result{IP: testIP, Seq: 0, Timestamp: now, Success: true, RTTNanos: 1000})
	pm.store.SaveResult(store.Result{IP: testIP, Seq: 1, Timestamp: now.Add(time.Second), Success: false, ErrMsg: "x"})
	pm.store.Sync()

	rec := do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: want 200, got %d", rec.Code)
	}
	body := decode(t, rec)
	if body["ip"] != testIP {
		t.Fatalf("history: wrong ip %v", body["ip"])
	}
	if cnt, _ := body["count"].(float64); cnt != 2 {
		t.Fatalf("history: expected count=2, got %v", body["count"])
	}

	// Limit is honored.
	rec = do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/history?limit=1", nil)
	body = decode(t, rec)
	if cnt, _ := body["count"].(float64); cnt != 1 {
		t.Fatalf("history limit: expected count=1, got %v", body["count"])
	}
}

func TestLegacyEndpoints(t *testing.T) {
	r, _ := newTestServer(t)

	// add
	rec := do(t, r, http.MethodPost, "/api/pinger/add", map[string]interface{}{"ips": []string{testIP}})
	if rec.Code != http.StatusOK || decode(t, rec)["success"] != true {
		t.Fatalf("legacy add failed: %d %s", rec.Code, rec.Body.String())
	}

	// host is now persisted and listed
	rec = do(t, r, http.MethodGet, "/api/hosts", nil)
	if ips := decode(t, rec)["ips"].([]interface{}); len(ips) != 1 {
		t.Fatalf("legacy add did not persist host: %v", ips)
	}

	// reset
	rec = do(t, r, http.MethodPost, "/api/pinger/reset", map[string]interface{}{"ips": []string{testIP}})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy reset failed: %d", rec.Code)
	}

	// remove
	rec = do(t, r, http.MethodPost, "/api/pinger/remove", map[string]interface{}{"ips": []string{testIP}})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy remove failed: %d", rec.Code)
	}
	rec = do(t, r, http.MethodGet, "/api/hosts", nil)
	if ips := decode(t, rec)["ips"].([]interface{}); len(ips) != 0 {
		t.Fatalf("legacy remove did not delete host: %v", ips)
	}
}

func TestLoadHostsResumesMonitoring(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "resume.db")

	// First store: persist a host, then close.
	st1, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error: %v", err)
	}
	if err := st1.AddHost(testIP); err != nil {
		t.Fatalf("AddHost() error: %v", err)
	}
	st1.Close()

	// Reopen and build a manager that should resume monitoring.
	st2, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	pm := NewPingerManager(st2)
	t.Cleanup(func() {
		pm.StopAll()
		st2.Close()
	})

	if err := pm.LoadHosts(); err != nil {
		t.Fatalf("LoadHosts() error: %v", err)
	}

	r := mux.NewRouter()
	registerAPIRoutes(r, pm)

	rec := do(t, r, http.MethodGet, "/api/pinger", nil)
	stats := map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &stats)
	if _, ok := stats[testIP]; !ok {
		t.Fatalf("LoadHosts did not resume pinger for %s: %v", testIP, stats)
	}
}
