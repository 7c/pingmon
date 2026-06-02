package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// uuid4LowerRe matches a lowercase RFC 4122 version-4 UUID.
var uuid4LowerRe = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// isValidUUID4Lower reports whether s is a lowercase uuid4.
func isValidUUID4Lower(s string) bool {
	return uuid4LowerRe.MatchString(s)
}

// multiToken is a repeatable string flag (--token a --token b) that validates
// each value as a lowercase uuid4 as it is parsed.
type multiToken []string

func (m *multiToken) String() string { return strings.Join(*m, ",") }

func (m *multiToken) Set(v string) error {
	v = strings.TrimSpace(v)
	if !isValidUUID4Lower(v) {
		return fmt.Errorf("token must be a lowercase uuid4, got %q", v)
	}
	*m = append(*m, v)
	return nil
}

// loadEnvTokens reads `token=` entries from a .env-style file. A missing file is
// not an error. Each value may be a comma-separated list, and the key may repeat
// across lines. Every value is validated as a lowercase uuid4.
func loadEnvTokens(path string) ([]string, error) {
	env, err := parseEnvFile(path)
	if err != nil {
		return nil, err
	}
	var tokens []string
	for _, raw := range env["token"] {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !isValidUUID4Lower(t) {
				return nil, fmt.Errorf("token in %q must be a lowercase uuid4, got %q", path, t)
			}
			tokens = append(tokens, t)
		}
	}
	return tokens, nil
}

// maskToken returns an identifiable but redacted form of a token for display
// (e.g. "3f2504e0…3301"), so the operator can tell which tokens are active
// without printing full secrets to the console/scrollback.
func maskToken(t string) string {
	if len(t) <= 12 {
		return "…"
	}
	return t[:8] + "…" + t[len(t)-4:]
}

// dedupeTokens returns the unique tokens preserving order.
func dedupeTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// authEnabled reports whether any token is configured. Exposed via /api/profile.
var authEnabled bool

// publicAPIPaths are reachable without a token even when auth is enabled.
var publicAPIPaths = map[string]bool{
	"/api/ping":    true,
	"/api/profile": true,
}

// requiresAuth reports whether a request path is subject to token auth. Only the
// API surface is protected; the public health/profile endpoints and non-/api
// paths are always reachable.
func requiresAuth(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	return !publicAPIPaths[path]
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// tokenAllowed compares the presented token against the configured set in
// constant time (no early exit) to avoid leaking timing information.
func tokenAllowed(token string, allowed []string) bool {
	match := 0
	tb := []byte(token)
	for _, a := range allowed {
		if subtle.ConstantTimeCompare(tb, []byte(a)) == 1 {
			match = 1
		}
	}
	return match == 1
}

// authMiddleware enforces Bearer-token auth on the API surface when tokens are
// configured. Unauthorized requests to protected paths return 403 Forbidden
// (404 is reserved for genuinely non-existent routes). With no tokens
// configured it is a no-op pass-through.
func authMiddleware(tokens []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(tokens) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requiresAuth(r.URL.Path) {
				tok, ok := bearerToken(r)
				if !ok || !tokenAllowed(tok, tokens) {
					debugf("auth: DENIED %s %s (bearer present=%t) -> 403", r.Method, r.URL.Path, ok)
					writeJSON(w, http.StatusForbidden, map[string]interface{}{
						"success": false, "error": "forbidden",
					})
					return
				}
				debugf("auth: allowed %s %s", r.Method, r.URL.Path)
			}
			next.ServeHTTP(w, r)
		})
	}
}
