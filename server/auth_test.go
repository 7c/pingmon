package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	validToken1 = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	validToken2 = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
)

func TestIsValidUUID4Lower(t *testing.T) {
	valid := []string{validToken1, validToken2}
	for _, s := range valid {
		if !isValidUUID4Lower(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	invalid := []string{
		"",
		"not-a-uuid",
		"3F2504E0-4F89-41D3-9A0C-0305E82C3301",       // uppercase
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301",       // version 1, not 4
		"3f2504e0-4f89-41d3-7a0c-0305e82c3301",       // bad variant (7)
		"3f2504e0-4f89-41d3-9a0c-0305e82c330",        // too short
		"3f2504e0-4f89-41d3-9a0c-0305e82c3301-extra", // too long
		"zf2504e0-4f89-41d3-9a0c-0305e82c3301",       // non-hex
	}
	for _, s := range invalid {
		if isValidUUID4Lower(s) {
			t.Errorf("expected %q invalid", s)
		}
	}
}

func TestMultiTokenFlag(t *testing.T) {
	var m multiToken
	if err := m.Set(validToken1); err != nil {
		t.Fatalf("valid token Set: %v", err)
	}
	if err := m.Set("BADTOKEN"); err == nil {
		t.Fatalf("expected error for invalid token")
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 token, got %d", len(m))
	}
}

func TestDedupeTokens(t *testing.T) {
	got := dedupeTokens([]string{validToken1, validToken2, validToken1})
	if len(got) != 2 || got[0] != validToken1 || got[1] != validToken2 {
		t.Fatalf("expected 2 unique tokens in order, got %v", got)
	}
}

func TestRequiresAuth(t *testing.T) {
	cases := map[string]bool{
		"/api/ping":       false,
		"/api/profile":    false,
		"/api/hosts":      true,
		"/api/hosts/full": true,
		"/api/status":     true,
		"/":               false,
		"/assets/app.js":  false,
		"/index.html":     false,
		"/api":            false, // not under /api/
	}
	for path, want := range cases {
		if got := requiresAuth(path); got != want {
			t.Errorf("requiresAuth(%q) = %v, want %v", path, got, want)
		}
	}
}

// nextOK is a trivial downstream handler that signals it was reached.
func nextOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("reached"))
	})
}

func TestAuthMiddlewareDisabledWhenNoTokens(t *testing.T) {
	h := authMiddleware(nil)(nextOK())
	req := httptest.NewRequest(http.MethodGet, "/api/hosts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no tokens: protected path should pass, got %d", rec.Code)
	}
}

func TestAuthMiddlewareEnforcement(t *testing.T) {
	tokens := []string{validToken1, validToken2}
	h := authMiddleware(tokens)(nextOK())

	call := func(path, authHeader string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Public health check: allowed without a token.
	if code := call("/api/ping", ""); code != http.StatusOK {
		t.Errorf("/api/ping without token: want 200, got %d", code)
	}
	// Static path: allowed without a token.
	if code := call("/index.html", ""); code != http.StatusOK {
		t.Errorf("/index.html without token: want 200, got %d", code)
	}
	// Protected, no header -> 403.
	if code := call("/api/hosts", ""); code != http.StatusForbidden {
		t.Errorf("/api/hosts no header: want 403, got %d", code)
	}
	// Protected, wrong token -> 403.
	if code := call("/api/hosts", "Bearer "+"00000000-0000-4000-8000-000000000000"); code != http.StatusForbidden {
		t.Errorf("/api/hosts wrong token: want 403, got %d", code)
	}
	// Protected, malformed header -> 403.
	if code := call("/api/hosts", "Token "+validToken1); code != http.StatusForbidden {
		t.Errorf("/api/hosts malformed scheme: want 403, got %d", code)
	}
	// Protected, valid token (each) -> 200.
	if code := call("/api/hosts", "Bearer "+validToken1); code != http.StatusOK {
		t.Errorf("/api/hosts valid token1: want 200, got %d", code)
	}
	if code := call("/api/status", "bearer "+validToken2); code != http.StatusOK {
		t.Errorf("/api/status valid token2 (lowercase scheme): want 200, got %d", code)
	}
}

// TestPingEndpointPublic confirms the real route is registered and open.
func TestPingEndpointPublic(t *testing.T) {
	r, _ := newTestServer(t)
	rec := do(t, r, http.MethodGet, "/api/ping", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/ping: want 200, got %d", rec.Code)
	}
	body := decode(t, rec)
	if body["status"] != "ok" {
		t.Fatalf("/api/ping: expected status ok, got %s", rec.Body.String())
	}
	if name, _ := body["name"].(string); name == "" {
		t.Fatalf("/api/ping: expected non-empty name, got %s", rec.Body.String())
	}
}

func TestDefaultServerName(t *testing.T) {
	if defaultServerName() == "" {
		t.Fatalf("defaultServerName must never be empty")
	}
}
