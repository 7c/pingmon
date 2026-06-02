package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsFullyOpen(t *testing.T) {
	h := corsMiddleware(nextOK())

	// Preflight OPTIONS from any origin -> 204 with permissive headers.
	req := httptest.NewRequest(http.MethodOptions, "/api/hosts", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: want 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin: want *, got %q", got)
	}
	// Requested headers are echoed back (covers Authorization).
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "authorization, content-type" {
		t.Fatalf("allow-headers: want echoed request, got %q", got)
	}

	// Actual GET from any origin -> wildcard header + reaches next.
	req = httptest.NewRequest(http.MethodGet, "/api/hosts", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("GET allow-origin: want *, got %q", got)
	}

	// No Origin / no requested headers still yields a permissive default.
	req = httptest.NewRequest(http.MethodGet, "/api/hosts", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("expected default allow-headers")
	}
}

func TestAuthUnauthorizedIs403(t *testing.T) {
	h := authMiddleware([]string{validToken1})(nextOK())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hosts", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unauthorized: want 403, got %d", rec.Code)
	}
}

func TestRootReturns404(t *testing.T) {
	r, _ := newTestServer(t)
	// "/" is intentionally unmapped so the service does not advertise itself.
	if rec := do(t, r, http.MethodGet, "/", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodGet, "/whatever", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /whatever: want 404, got %d", rec.Code)
	}
}

func TestProfileEndpoint(t *testing.T) {
	r, _ := newTestServer(t)
	rec := do(t, r, http.MethodGet, "/api/profile", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/profile: want 200, got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	for _, k := range []string{"name", "service", "version", "authRequired", "serverTime", "defaults"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("profile missing %q: %s", k, rec.Body.String())
		}
	}
	if body["service"] != "pingmon" {
		t.Fatalf("profile service: got %v", body["service"])
	}
	defaults, ok := body["defaults"].(map[string]interface{})
	if !ok || defaults["intervalMs"] == nil {
		t.Fatalf("profile defaults missing fields: %s", rec.Body.String())
	}
}
