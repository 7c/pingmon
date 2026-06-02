package main

import "net/http"

// corsMiddleware applies fully-open CORS: any origin, any method, any header,
// with no checks. The multi-backend UI connects to arbitrary server URLs
// cross-origin, and auth uses a bearer header (not cookies), so a wildcard
// origin is safe. Preflight OPTIONS requests are answered with 204.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
		// Echo whatever headers the client asked for (covers Authorization,
		// which a bare "*" does not), falling back to "*".
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			h.Set("Access-Control-Allow-Headers", req)
		} else {
			h.Set("Access-Control-Allow-Headers", "*, Authorization")
		}
		h.Set("Access-Control-Max-Age", "600")

		// Short-circuit preflight before auth/routing.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
