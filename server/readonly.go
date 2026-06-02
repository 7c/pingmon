package main

import (
	"net/http"
	"strings"
)

// readOnlyEnabled freezes all write operations when true (set from --readonly).
// Reads keep working, so the UI can view (and live-poll) hosts/groups/history
// without being able to change anything. Used for an admin "freeze" or a public
// demo with realtime data.
var readOnlyEnabled bool

// isWriteMethod reports whether an HTTP method mutates state. Every mutating API
// endpoint uses one of these; GET/HEAD/OPTIONS are reads.
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// readonlyMiddleware blocks mutating /api requests with 423 Locked when
// read-only mode is enabled, returning a body the frontend can detect
// (readOnly: true). It is a no-op when read-only mode is off.
func readonlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if readOnlyEnabled && isWriteMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") {
			debugf("readonly: blocked %s %s -> 423", r.Method, r.URL.Path)
			writeJSON(w, http.StatusLocked, map[string]interface{}{
				"success":  false,
				"error":    "read-only mode",
				"readOnly": true,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
