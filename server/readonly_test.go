package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadonlyMiddleware(t *testing.T) {
	prev := readOnlyEnabled
	defer func() { readOnlyEnabled = prev }()

	h := readonlyMiddleware(nextOK())

	// Disabled: writes pass through.
	readOnlyEnabled = false
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hosts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readonly off: POST should pass, got %d", rec.Code)
	}

	// Enabled: reads pass, writes are blocked with 423 + readOnly flag.
	readOnlyEnabled = true

	if rec := serve(h, http.MethodGet, "/api/hosts"); rec.Code != http.StatusOK {
		t.Errorf("GET should pass in read-only, got %d", rec.Code)
	}

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := serve(h, m, "/api/hosts")
		if rec.Code != http.StatusLocked {
			t.Errorf("%s in read-only: want 423, got %d", m, rec.Code)
		}
		var body map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body["readOnly"] != true {
			t.Errorf("%s: expected readOnly=true body, got %s", m, rec.Body.String())
		}
	}

	// Non-/api writes are not gated by this middleware.
	if rec := serve(h, http.MethodPost, "/something"); rec.Code != http.StatusOK {
		t.Errorf("non-api POST should pass, got %d", rec.Code)
	}
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}
