package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/7c/pingmon/classes/store"
)

// rangeQuery builds a ?start=&end=&resolution= query string for the last d.
func rangeQuery(d time.Duration, resolution string) string {
	end := time.Now().UTC()
	start := end.Add(-d)
	v := url.Values{}
	v.Set("start", start.Format(time.RFC3339))
	v.Set("end", end.Format(time.RFC3339))
	if resolution != "" {
		v.Set("resolution", resolution)
	}
	return "?" + v.Encode()
}

func TestCreateHostWithConfigAndFull(t *testing.T) {
	r, _ := newTestServer(t)

	body := map[string]interface{}{
		"ip":          testIP,
		"config":      map[string]int{"intervalMs": 5000, "timeoutMs": 3000, "packetSize": 64},
		"displayName": "loopback",
		"tags":        []string{"local", "test"},
		"notes":       "unit test host",
	}
	rec := do(t, r, http.MethodPost, "/api/hosts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = do(t, r, http.MethodGet, "/api/hosts/full", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("full: want 200, got %d", rec.Code)
	}
	var resp struct {
		Hosts []store.Host `json:"hosts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode hosts/full: %v", err)
	}
	if len(resp.Hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(resp.Hosts))
	}
	h := resp.Hosts[0]
	if h.Config.IntervalMs != 5000 || h.DisplayName != "loopback" || len(h.Tags) != 2 {
		t.Fatalf("host metadata not applied: %+v", h)
	}
}

func TestCreateHostInvalidConfig(t *testing.T) {
	r, _ := newTestServer(t)
	// A too-small interval is clamped (not rejected); use an out-of-bounds
	// timeout to exercise validation failure.
	body := map[string]interface{}{
		"ip":     testIP,
		"config": map[string]int{"intervalMs": 1000, "timeoutMs": 999999, "packetSize": 64}, // timeout too large
	}
	rec := do(t, r, http.MethodPost, "/api/hosts", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad config, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUpdateConfigMetaAlerts(t *testing.T) {
	r, pm := newTestServer(t)
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	// Config
	rec := do(t, r, http.MethodPut, "/api/hosts/"+testIP+"/config",
		map[string]int{"intervalMs": 2000, "timeoutMs": 1500, "packetSize": 32})
	if rec.Code != http.StatusOK {
		t.Fatalf("update config: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	h, _ := pm.store.GetHost(testIP)
	if h.Config.IntervalMs != 2000 || h.Config.PacketSize != 32 {
		t.Fatalf("config not persisted: %+v", h.Config)
	}

	// Bad config -> 400
	rec = do(t, r, http.MethodPut, "/api/hosts/"+testIP+"/config",
		map[string]int{"intervalMs": 999999999, "timeoutMs": 1500, "packetSize": 32})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}

	// Meta + tags
	rec = do(t, r, http.MethodPut, "/api/hosts/"+testIP+"/meta",
		map[string]interface{}{"displayName": "lo", "notes": "n", "tags": []string{"x"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update meta: want 200, got %d", rec.Code)
	}
	h, _ = pm.store.GetHost(testIP)
	if h.DisplayName != "lo" || len(h.Tags) != 1 {
		t.Fatalf("meta not persisted: %+v", h)
	}

	// Alerts
	rec = do(t, r, http.MethodPut, "/api/hosts/"+testIP+"/alerts",
		map[string]interface{}{"latencyMs": 100, "lossPct": 10.0})
	if rec.Code != http.StatusOK {
		t.Fatalf("update alerts: want 200, got %d", rec.Code)
	}
	h, _ = pm.store.GetHost(testIP)
	if h.AlertLatencyMs != 100 || h.AlertLossPct != 10.0 {
		t.Fatalf("alerts not persisted: %+v", h)
	}

	// Update config on missing host -> 404
	rec = do(t, r, http.MethodPut, "/api/hosts/9.9.9.9/config",
		map[string]int{"intervalMs": 2000, "timeoutMs": 1500, "packetSize": 32})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for missing host, got %d", rec.Code)
	}
}

func TestSeriesEndpoints(t *testing.T) {
	r, pm := newTestServer(t)
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	start := time.Now().UTC().Add(-2 * time.Minute)
	for i := 0; i < 60; i++ {
		pm.store.SaveResult(store.Result{IP: testIP, Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: true, RTTNanos: 10 * int64(time.Millisecond)})
	}
	pm.store.Sync()

	// Single-host series via history endpoint.
	rec := do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/history"+rangeQuery(10*time.Minute, "minute"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("series: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var series store.Series
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if series.Resolution != "minute" || len(series.Buckets) == 0 {
		t.Fatalf("unexpected series: %+v", series)
	}

	// Legacy history shape still works with limit only.
	rec = do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/history?limit=5", nil)
	body := decode(t, rec)
	if _, ok := body["results"]; !ok {
		t.Fatalf("legacy history shape missing results: %v", body)
	}

	// Multi-IP series.
	rec = do(t, r, http.MethodGet, "/api/series?ips="+testIP+"&resolution=minute"+"&"+rangeQuery(10*time.Minute, "")[1:], nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("multi-series: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var multi struct {
		Series []store.Series `json:"series"`
	}
	json.Unmarshal(rec.Body.Bytes(), &multi)
	if len(multi.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(multi.Series))
	}

	// Bad range -> 400.
	rec = do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/history?start=not-a-time&resolution=raw", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad time, got %d", rec.Code)
	}
}

func TestStatusEndpoint(t *testing.T) {
	r, pm := newTestServer(t)
	// Add the host directly so no live (failing) loopback pinger pollutes the
	// seeded results that drive status classification.
	if err := pm.store.AddHost(testIP); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		pm.store.SaveResult(store.Result{IP: testIP, Seq: i, Timestamp: now.Add(time.Duration(i-10) * time.Second), Success: true, RTTNanos: int64(time.Millisecond)})
	}
	pm.store.Sync()

	rec := do(t, r, http.MethodGet, "/api/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	var resp struct {
		Hosts []store.HostStatus `json:"hosts"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Hosts) != 1 || resp.Hosts[0].State != store.StateUp {
		t.Fatalf("unexpected status: %+v", resp.Hosts)
	}
}

func TestGroupsHandlers(t *testing.T) {
	r, _ := newTestServer(t)
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	// Create
	rec := do(t, r, http.MethodPost, "/api/groups", map[string]interface{}{"name": "edge", "color": "#abc"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var g store.Group
	json.Unmarshal(rec.Body.Bytes(), &g)
	gid := fmt.Sprintf("%d", g.ID)

	// Duplicate name -> 409
	rec = do(t, r, http.MethodPost, "/api/groups", map[string]interface{}{"name": "edge"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 for duplicate, got %d", rec.Code)
	}

	// List
	rec = do(t, r, http.MethodGet, "/api/groups", nil)
	var list struct {
		Groups []store.Group `json:"groups"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(list.Groups))
	}

	// Assign host
	rec = do(t, r, http.MethodPost, "/api/groups/"+gid+"/hosts", map[string]interface{}{"ip": testIP})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: want 200, got %d", rec.Code)
	}
	rec = do(t, r, http.MethodGet, "/api/groups/"+gid+"/hosts", nil)
	ips := decode(t, rec)["ips"].([]interface{})
	if len(ips) != 1 {
		t.Fatalf("expected 1 host in group, got %v", ips)
	}

	// Health
	rec = do(t, r, http.MethodGet, "/api/groups/"+gid+"/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health: want 200, got %d", rec.Code)
	}

	// Unassign
	rec = do(t, r, http.MethodDelete, "/api/groups/"+gid+"/hosts/"+testIP, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unassign: want 200, got %d", rec.Code)
	}

	// Update + delete
	rec = do(t, r, http.MethodPut, "/api/groups/"+gid, map[string]interface{}{"name": "edge2", "description": "x"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update group: want 200, got %d", rec.Code)
	}
	rec = do(t, r, http.MethodDelete, "/api/groups/"+gid, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete group: want 200, got %d", rec.Code)
	}
	rec = do(t, r, http.MethodGet, "/api/groups/"+gid, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", rec.Code)
	}
}

func TestAnnotationsHandlers(t *testing.T) {
	r, _ := newTestServer(t)
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	// Place the annotation a minute in the past so it sits safely inside the
	// last-hour list window (the overlap query treats `end` as exclusive).
	now := time.Now().UTC().Add(-time.Minute)
	rec := do(t, r, http.MethodPost, "/api/hosts/"+testIP+"/annotations", map[string]interface{}{
		"type": "incident", "title": "spike", "text": "loss", "startTs": now.Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create annotation: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var a store.Annotation
	json.Unmarshal(rec.Body.Bytes(), &a)
	aid := fmt.Sprintf("%d", a.ID)

	// Missing startTs -> 400
	rec = do(t, r, http.MethodPost, "/api/hosts/"+testIP+"/annotations", map[string]interface{}{"title": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing startTs, got %d", rec.Code)
	}

	// List overlapping window
	rec = do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/annotations"+rangeQuery(time.Hour, ""), nil)
	var list struct {
		Annotations []store.Annotation `json:"annotations"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(list.Annotations))
	}

	// Update + delete
	rec = do(t, r, http.MethodPut, "/api/annotations/"+aid, map[string]interface{}{
		"title": "updated", "startTs": now.Format(time.RFC3339),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update annotation: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = do(t, r, http.MethodDelete, "/api/annotations/"+aid, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete annotation: want 200, got %d", rec.Code)
	}
	rec = do(t, r, http.MethodPut, "/api/annotations/"+aid, map[string]interface{}{"title": "x", "startTs": now.Format(time.RFC3339)})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", rec.Code)
	}
}

func TestAnalyticsHandlers(t *testing.T) {
	r, pm := newTestServer(t)
	do(t, r, http.MethodPost, "/api/hosts", map[string]interface{}{"ip": testIP})

	start := time.Now().UTC().Add(-2 * time.Minute)
	for i := 0; i < 100; i++ {
		success := i%4 != 0
		rec := store.Result{IP: testIP, Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: success}
		if success {
			rec.RTTNanos = int64(i+1) * int64(time.Millisecond)
		}
		pm.store.SaveResult(rec)
	}
	pm.store.Sync()

	for _, path := range []string{"availability", "percentiles", "outages"} {
		rec := do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/"+path+rangeQuery(10*time.Minute, ""), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d (%s)", path, rec.Code, rec.Body.String())
		}
		// Bad range -> 400
		rec = do(t, r, http.MethodGet, "/api/hosts/"+testIP+"/"+path+"?start=bad&end=also-bad", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s bad range: want 400, got %d", path, rec.Code)
		}
	}
}
