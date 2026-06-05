package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"

	"github.com/7c/pingmon/classes/arp"
	"github.com/7c/pingmon/classes/pinger"
	"github.com/7c/pingmon/classes/rollup"
	"github.com/7c/pingmon/classes/store"
)

// ErrorInfo represents ping error information for the frontend
type ErrorInfo struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}

// EnhancedStats includes regular ping stats plus errors information
type EnhancedStats struct {
	pinger.Stats
	ErrorEvents []ErrorInfo `json:"errorEvents"`
}

// PingerManager handles multiple pingers
type PingerManager struct {
	mutex   sync.RWMutex
	pingers map[string]*pinger.Pinger
	store   *store.Store
}

// NewPingerManager creates a new pinger manager backed by the given store.
func NewPingerManager(s *store.Store) *PingerManager {
	return &PingerManager{
		pingers: make(map[string]*pinger.Pinger),
		store:   s,
	}
}

// defaultDataFolder is where persistent data lives unless overridden by
// --datafolder or the config file. Created on startup if missing.
const defaultDataFolder = "/var/lib/pingmon"

// dbFileName is the fixed database filename inside the data folder.
const dbFileName = "pingmon.db"

// minPingIntervalMs is the server-enforced floor for a host's ping interval.
// Any configured interval below this is clamped up. Set from
// --min-ping-interval / config minimum_ping_interval (default 500ms).
var minPingIntervalMs = 500

// clampConfig raises a config's interval to the enforced minimum. Returns a copy.
func clampConfig(cfg store.HostConfig) store.HostConfig {
	if cfg.IntervalMs < minPingIntervalMs {
		cfg.IntervalMs = minPingIntervalMs
	}
	return cfg
}

// optionsFromConfig builds continuous pinger options from a stored per-host
// config (durations in milliseconds), enforcing the minimum interval.
func optionsFromConfig(cfg store.HostConfig) pinger.PingerOptions {
	cfg = clampConfig(cfg)
	return pinger.PingerOptions{
		Timeout:  time.Duration(cfg.TimeoutMs) * time.Millisecond,
		Count:    pinger.UnlimitedCount,
		Size:     cfg.PacketSize,
		Interval: time.Duration(cfg.IntervalMs) * time.Millisecond,
	}
}

// startPinger creates, configures, and starts a pinger for ip using cfg. The
// caller must hold pm.mutex.
func (pm *PingerManager) startPinger(ip string, cfg store.HostConfig) (*pinger.Pinger, error) {
	p, err := pinger.NewPingerWithOptions(ip, optionsFromConfig(cfg))
	if err != nil {
		return nil, err
	}

	// Persist every result for long-term history.
	p.SetResultHandler(func(r pinger.PingResult) {
		rec := store.Result{
			IP:        ip,
			Seq:       r.Seq,
			Timestamp: r.Timestamp,
			Success:   r.Success,
			RTTNanos:  r.RTT.Nanoseconds(),
		}
		if r.Error != nil {
			rec.ErrMsg = r.Error.Error()
		}
		pm.store.SaveResult(rec)
	})

	p.Run()
	return p, nil
}

// AddPinger adds a new pinger for the given IP using default config.
func (pm *PingerManager) AddPinger(ip string) error {
	return pm.AddPingerWithConfig(ip, store.DefaultHostConfig())
}

// AddPingerWithConfig adds a new pinger for the given IP with a specific config
// and persists it as a monitored host so it resumes after a restart.
func (pm *PingerManager) AddPingerWithConfig(ip string, cfg store.HostConfig) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Check if pinger already exists
	if _, exists := pm.pingers[ip]; exists {
		return nil // Already exists
	}

	p, err := pm.startPinger(ip, cfg)
	if err != nil {
		return err
	}

	if err := pm.store.AddHostWithConfig(ip, cfg); err != nil {
		// Persistence failed: stop the pinger so state stays consistent.
		p.Stop()
		return err
	}

	pm.pingers[ip] = p
	return nil
}

// UpdatePingerConfig persists a new config for the host and, if it is currently
// running, restarts its pinger with the new settings.
func (pm *PingerManager) UpdatePingerConfig(ip string, cfg store.HostConfig) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if err := pm.store.UpdateHostConfig(ip, cfg); err != nil {
		return err
	}

	if p, exists := pm.pingers[ip]; exists {
		p.Stop()
		np, err := pm.startPinger(ip, cfg)
		if err != nil {
			delete(pm.pingers, ip)
			return err
		}
		pm.pingers[ip] = np
	}
	return nil
}

// LoadHosts resumes monitoring for every host previously persisted in the
// store, using each host's stored config. Called once at startup.
func (pm *PingerManager) LoadHosts() error {
	hosts, err := pm.store.ListHostsFull()
	if err != nil {
		return err
	}

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, h := range hosts {
		if _, exists := pm.pingers[h.IP]; exists {
			continue
		}
		p, err := pm.startPinger(h.IP, h.Config)
		if err != nil {
			log.Printf("[STARTUP] failed to resume pinger for %s: %v", h.IP, err)
			continue
		}
		pm.pingers[h.IP] = p
		log.Printf("[STARTUP] resumed monitoring %s (interval %dms)", h.IP, h.Config.IntervalMs)
	}
	return nil
}

// RemovePinger removes and stops a pinger. Historical ping results are kept in
// the store for long-term analysis.
func (pm *PingerManager) RemovePinger(ip string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if p, exists := pm.pingers[ip]; exists {
		p.Stop()
		delete(pm.pingers, ip)
		if err := pm.store.RemoveHost(ip); err != nil {
			log.Printf("[STORE] failed to remove host %s: %v", ip, err)
		}
	}
}

// StopAll stops every running pinger without touching persisted state. Used on
// graceful shutdown.
func (pm *PingerManager) StopAll() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, p := range pm.pingers {
		p.Stop()
	}
}

// Count returns the number of currently managed (monitored) hosts.
func (pm *PingerManager) Count() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return len(pm.pingers)
}

// configPathIf returns path when a config file was found, else "" (banner uses
// this to show whether a config file was loaded).
func configPathIf(found bool, path string) string {
	if found {
		return path
	}
	return ""
}

// resolveDBPath returns the database file path: always <dataFolder>/pingmon.db.
func resolveDBPath(dataFolder string) string {
	return filepath.Join(dataFolder, dbFileName)
}

// GetStats returns statistics for all pingers
func (pm *PingerManager) GetStats() map[string]EnhancedStats {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := make(map[string]EnhancedStats)
	for ip, p := range pm.pingers {
		baseStats := p.GetStatistics()

		// Create enhanced stats with error events
		enhancedStats := EnhancedStats{
			Stats:       baseStats,
			ErrorEvents: make([]ErrorInfo, 0),
		}

		// Add error information if available
		if baseStats.ErrorsCount > 0 && len(baseStats.Errors) > 0 {
			// Get current time to estimate timestamps for errors
			now := time.Now()

			// Calculate approximate timestamps for errors
			// We don't have exact timestamps so we'll space them evenly from recent history
			for i, err := range baseStats.Errors {
				if err != nil {
					// Create error events spaced roughly equally from now backwards
					// This is an approximation since we don't store exact error timestamps
					timeDelta := time.Duration(len(baseStats.Errors)-i) * time.Second * 5
					errorTime := now.Add(-timeDelta)

					enhancedStats.ErrorEvents = append(enhancedStats.ErrorEvents, ErrorInfo{
						Timestamp: errorTime,
						Message:   err.Error(),
					})
				}
			}
		}

		stats[ip] = enhancedStats
	}

	return stats
}

// ResetPingers stops and restarts specified pingers, clearing their stats while
// preserving each host's stored per-IP configuration.
func (pm *PingerManager) ResetPingers(ips []string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, ip := range ips {
		p, exists := pm.pingers[ip]
		if !exists {
			continue
		}
		p.Stop()

		// Reuse the host's stored config so a reset never silently reverts
		// interval/timeout/size to defaults.
		cfg := store.DefaultHostConfig()
		if h, err := pm.store.GetHost(ip); err == nil {
			cfg = h.Config
		}

		newPinger, err := pm.startPinger(ip, cfg)
		if err != nil {
			delete(pm.pingers, ip)
			log.Printf("[RESET] failed to restart pinger for %s: %v", ip, err)
			continue
		}
		pm.pingers[ip] = newPinger
	}
}

// RemovePingers completely removes the specified pingers
func (pm *PingerManager) RemovePingers(ips []string) {
	for _, ip := range ips {
		pm.RemovePinger(ip)
	}
}

// API request/response types
type AddPingerRequest struct {
	IPs []string `json:"ips"`
}

type ResetPingerRequest struct {
	IPs []string `json:"ips"`
}

type RemovePingerRequest struct {
	IPs []string `json:"ips"`
}

// CreateHostRequest accepts either a single ip or a list of ips, plus optional
// config and metadata applied to every created host.
type CreateHostRequest struct {
	IP          string            `json:"ip"`
	IPs         []string          `json:"ips"`
	Config      *store.HostConfig `json:"config,omitempty"`
	DisplayName string            `json:"displayName"`
	Tags        []string          `json:"tags"`
	Notes       string            `json:"notes"`
}

// Per-IP config validation bounds.
const (
	minIntervalMs = 100
	maxIntervalMs = 3600000 // 1h
	minTimeoutMs  = 100
	maxTimeoutMs  = 60000 // 60s
	minPacketSize = 8
	maxPacketSize = 65500
)

// validateConfig checks per-IP ping configuration bounds. It returns a
// user-facing error message, or "" when valid.
func validateConfig(cfg store.HostConfig) string {
	if cfg.IntervalMs < minIntervalMs || cfg.IntervalMs > maxIntervalMs {
		return fmt.Sprintf("intervalMs must be between %d and %d", minIntervalMs, maxIntervalMs)
	}
	if cfg.TimeoutMs < minTimeoutMs || cfg.TimeoutMs > maxTimeoutMs {
		return fmt.Sprintf("timeoutMs must be between %d and %d", minTimeoutMs, maxTimeoutMs)
	}
	if cfg.PacketSize < minPacketSize || cfg.PacketSize > maxPacketSize {
		return fmt.Sprintf("packetSize must be between %d and %d", minPacketSize, maxPacketSize)
	}
	return ""
}

// writeStoreError maps store errors to HTTP responses.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "conflict"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
	}
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[API] failed to encode response: %v", err)
	}
}

// GetHistory returns persisted ping results for an IP, newest first.
func (pm *PingerManager) GetHistory(ip string, limit int) ([]store.Result, error) {
	return pm.store.GetResults(ip, limit)
}

// --- HTTP handlers ---

// handlePing is a public health check that never requires authentication. It
// reports the server name so clients can identify the instance.
func (pm *PingerManager) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"name":   serverName,
	})
}

// handleProfile is a public endpoint returning this instance's metadata and
// defaults, used by the multi-backend UI when registering a server.
func (pm *PingerManager) handleProfile(w http.ResponseWriter, r *http.Request) {
	cfg := store.DefaultHostConfig()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":          serverName,
		"service":       "pingmon",
		"version":       appVersion,
		"authRequired":  authEnabled,
		"readOnly":      readOnlyEnabled,
		"arpScan":       arpEnabled,
		"minIntervalMs": minPingIntervalMs,
		"serverTime":    time.Now().UTC(),
		"defaults": map[string]int{
			"intervalMs": cfg.IntervalMs,
			"timeoutMs":  cfg.TimeoutMs,
			"packetSize": cfg.PacketSize,
		},
	})
}

func (pm *PingerManager) handleGetStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, pm.GetStats())
}

// handleListHosts returns the set of currently monitored IPs (CRUD: Read).
func (pm *PingerManager) handleListHosts(w http.ResponseWriter, r *http.Request) {
	ips, err := pm.store.ListHosts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	if ips == nil {
		ips = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ips": ips})
}

// handleCreateHost adds one or more monitored IPs (CRUD: Create).
func (pm *PingerManager) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var req CreateHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	ips := req.IPs
	if req.IP != "" {
		ips = append(ips, req.IP)
	}
	if len(ips) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "no ip(s) provided",
		})
		return
	}

	// Resolve config: provided values merged onto defaults, clamped to the
	// enforced minimum interval, then validated.
	cfg := store.DefaultHostConfig()
	if req.Config != nil {
		cfg = *req.Config
	}
	cfg = clampConfig(cfg)
	if msg := validateConfig(cfg); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": msg})
		return
	}

	errs := make(map[string]string)
	for _, ip := range ips {
		if err := pm.AddPingerWithConfig(ip, cfg); err != nil {
			errs[ip] = err.Error()
			continue
		}
		// Best-effort metadata; failures here don't fail the add.
		if req.DisplayName != "" || req.Notes != "" {
			if err := pm.store.UpdateHostMeta(ip, req.DisplayName, req.Notes); err != nil {
				log.Printf("[API] set meta for %s: %v", ip, err)
			}
		}
		if len(req.Tags) > 0 {
			if err := pm.store.SetHostTags(ip, req.Tags); err != nil {
				log.Printf("[API] set tags for %s: %v", ip, err)
			}
		}
	}

	if len(errs) > 0 {
		writeJSON(w, http.StatusPartialContent, map[string]interface{}{
			"success": len(errs) < len(ips),
			"errors":  errs,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"success": true})
}

// handleDeleteHost removes a single monitored IP (CRUD: Delete).
func (pm *PingerManager) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	pm.RemovePinger(ip)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleResetHost resets statistics for a single IP (CRUD: Update).
func (pm *PingerManager) handleResetHost(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	pm.ResetPingers([]string{ip})
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleGetHistory returns persisted ping results for a single IP. When any of
// start/end/resolution query params are present it returns an aggregated Series;
// otherwise it returns the legacy {ip,count,results} shape (limit param).
func (pm *PingerManager) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	q := r.URL.Query()

	if q.Get("start") != "" || q.Get("end") != "" || q.Get("resolution") != "" {
		start, end, err := parseRange(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		series, err := pm.store.GetSeries(ip, start, end, q.Get("resolution"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, series)
		return
	}

	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	results, err := pm.GetHistory(ip, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ip":      ip,
		"count":   len(results),
		"results": results,
	})
}

// --- Legacy /api/pinger handlers (kept for frontend compatibility) ---

func (pm *PingerManager) handleAddPinger(w http.ResponseWriter, r *http.Request) {
	var req AddPingerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	errs := make(map[string]string)
	for _, ip := range req.IPs {
		if err := pm.AddPinger(ip); err != nil {
			errs[ip] = err.Error()
		}
	}

	if len(errs) > 0 {
		writeJSON(w, http.StatusPartialContent, map[string]interface{}{
			"success": len(errs) < len(req.IPs),
			"errors":  errs,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (pm *PingerManager) handleResetPinger(w http.ResponseWriter, r *http.Request) {
	var req ResetPingerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pm.ResetPingers(req.IPs)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (pm *PingerManager) handleRemovePinger(w http.ResponseWriter, r *http.Request) {
	var req RemovePingerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pm.RemovePingers(req.IPs)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// registerAPIRoutes mounts all /api routes onto r.
func registerAPIRoutes(r *mux.Router, pm *PingerManager) {
	api := r.PathPrefix("/api").Subrouter()

	// Public endpoints (never require auth)
	api.HandleFunc("/ping", pm.handlePing).Methods(http.MethodGet)
	api.HandleFunc("/profile", pm.handleProfile).Methods(http.MethodGet)

	// Stats
	api.HandleFunc("/pinger", pm.handleGetStats).Methods(http.MethodGet)

	// Hosts CRUD + metadata. Register the more specific /hosts/full before the
	// /hosts/{ip} pattern so it is not captured as an IP.
	api.HandleFunc("/hosts", pm.handleListHosts).Methods(http.MethodGet)
	api.HandleFunc("/hosts", pm.handleCreateHost).Methods(http.MethodPost)
	api.HandleFunc("/hosts/full", pm.handleListHostsFull).Methods(http.MethodGet)
	api.HandleFunc("/hosts/{ip}", pm.handleGetHost).Methods(http.MethodGet)
	api.HandleFunc("/hosts/{ip}", pm.handleDeleteHost).Methods(http.MethodDelete)
	api.HandleFunc("/hosts/{ip}/config", pm.handleUpdateConfig).Methods(http.MethodPut)
	api.HandleFunc("/hosts/{ip}/meta", pm.handleUpdateMeta).Methods(http.MethodPut)
	api.HandleFunc("/hosts/{ip}/alerts", pm.handleUpdateAlerts).Methods(http.MethodPut)
	api.HandleFunc("/hosts/{ip}/reset", pm.handleResetHost).Methods(http.MethodPost)
	api.HandleFunc("/hosts/{ip}/history", pm.handleGetHistory).Methods(http.MethodGet)

	// Analytics
	api.HandleFunc("/hosts/{ip}/availability", pm.handleAvailability).Methods(http.MethodGet)
	api.HandleFunc("/hosts/{ip}/percentiles", pm.handlePercentiles).Methods(http.MethodGet)
	api.HandleFunc("/hosts/{ip}/outages", pm.handleOutages).Methods(http.MethodGet)

	// Annotations
	api.HandleFunc("/hosts/{ip}/annotations", pm.handleListAnnotations).Methods(http.MethodGet)
	api.HandleFunc("/hosts/{ip}/annotations", pm.handleCreateAnnotation).Methods(http.MethodPost)
	api.HandleFunc("/annotations/{id}", pm.handleUpdateAnnotation).Methods(http.MethodPut)
	api.HandleFunc("/annotations/{id}", pm.handleDeleteAnnotation).Methods(http.MethodDelete)

	// Multi-IP series (overlay) + status wall
	api.HandleFunc("/series", pm.handleSeries).Methods(http.MethodGet)
	api.HandleFunc("/status", pm.handleStatus).Methods(http.MethodGet)

	// Groups
	api.HandleFunc("/groups", pm.handleListGroups).Methods(http.MethodGet)
	api.HandleFunc("/groups", pm.handleCreateGroup).Methods(http.MethodPost)
	api.HandleFunc("/groups/{id}", pm.handleGetGroup).Methods(http.MethodGet)
	api.HandleFunc("/groups/{id}", pm.handleUpdateGroup).Methods(http.MethodPut)
	api.HandleFunc("/groups/{id}", pm.handleDeleteGroup).Methods(http.MethodDelete)
	api.HandleFunc("/groups/{id}/hosts", pm.handleListGroupHosts).Methods(http.MethodGet)
	api.HandleFunc("/groups/{id}/hosts", pm.handleAssignGroupHosts).Methods(http.MethodPost)
	api.HandleFunc("/groups/{id}/hosts/{ip}", pm.handleUnassignGroupHost).Methods(http.MethodDelete)
	api.HandleFunc("/groups/{id}/health", pm.handleGroupHealth).Methods(http.MethodGet)

	// Legacy endpoints (kept for the existing frontend)
	api.HandleFunc("/pinger/add", pm.handleAddPinger).Methods(http.MethodPost)
	api.HandleFunc("/pinger/reset", pm.handleResetPinger).Methods(http.MethodPost)
	api.HandleFunc("/pinger/remove", pm.handleRemovePinger).Methods(http.MethodPost)
}

// loggingMiddleware logs one concise completion line per request. In debug mode
// it additionally logs an incoming-request line with selected headers.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if debugEnabled {
			_, hasBearer := bearerToken(r)
			debugf(
				"--> %s %s from %s | UA=%q | bearer=%t | len=%d",
				r.Method, r.RequestURI, r.RemoteAddr,
				r.Header.Get("User-Agent"), hasBearer, r.ContentLength,
			)
		}

		// Create a custom ResponseWriter to capture status code
		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			StatusCode:     http.StatusOK,
		}

		// Call the next handler
		next.ServeHTTP(lrw, r)

		// Log the response details
		duration := time.Since(start)
		log.Printf(
			"[%s] %s %s %s - %d in %v",
			time.Now().Format("2006-01-02 15:04:05"),
			r.Method,
			r.RequestURI,
			r.RemoteAddr,
			lrw.StatusCode,
			duration,
		)
	})
}

// Custom response writer to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	StatusCode int
}

// Implement WriteHeader to capture status code
func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.StatusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Command line flags
var (
	portFlag           = flag.Int("port", 6868, "Port to run the server on")
	dataFolderFlag     = flag.String("datafolder", defaultDataFolder, "Directory for persistent data (database, etc.); created if missing. The database is always <datafolder>/pingmon.db")
	rawRetainFlag      = flag.Duration("raw-retain", 720*time.Hour, "How long to keep raw ping results before pruning (0 = keep forever)")
	rollupFlag         = flag.Duration("rollup-interval", 5*time.Minute, "How often the background rollup job runs")
	configFlag         = flag.String("config", defaultConfigPath, "Path to the config file (KEY=VALUE; see `pingmon config`)")
	debugFlag          = flag.Bool("debug", false, "Enable verbose debug logging (per-ping output, auth decisions, request/rollup detail)")
	nameFlag           = flag.String("name", "", "Server name used to identify this instance (for profiling); defaults to the OS hostname")
	readonlyFlag       = flag.Bool("readonly", false, "Read-only mode: block all write operations (writes return 423); for freezing data or a demo")
	arpscanFlag        = flag.Bool("arpscan", false, "Enable periodic ARP scanning of all interfaces, exposed at GET /api/arp")
	arpIntervalF       = flag.Duration("arp-interval", 0, "Passive ARP-cache read interval (or config arp_interval; default 2m)")
	arpActiveFlag      = flag.Bool("arp-active", true, "Actively sweep each interface's IPv4 subnet (arp-scan -l style; Linux+root, falls back to passive)")
	arpActiveIntervalF = flag.Duration("arp-active-interval", 0, "How often the active ARP sweep runs (or config arp_active_interval; default 10m)")
	arpActiveMaxF      = flag.Int("arp-active-max-hosts", 256, "Skip active sweep of subnets larger than this many addresses (~/24); or config arp_active_max_hosts")
	minPingFlag        = flag.Duration("min-ping-interval", 0, "Minimum enforced ping interval; lower values are clamped up (or config minimum_ping_interval; default 500ms)")
	tokenFlags         multiToken
	hostFlags          multiHost
)

// maxListenHosts caps how many --host values (IPs or interface names) are accepted.
const maxListenHosts = 3

// multiHost is a repeatable --host flag accepting IPs or interface names
// (comma-separated values are also split).
type multiHost []string

func (m *multiHost) String() string { return strings.Join(*m, ",") }

func (m *multiHost) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*m = append(*m, p)
		}
	}
	return nil
}

// hostToIPs resolves a --host entry to listen IPs: an IP literal is used as-is;
// otherwise it is treated as an interface name and expanded to its non-loopback,
// non-link-local addresses.
func hostToIPs(h string) ([]string, error) {
	if net.ParseIP(h) != nil {
		return []string{h}, nil // IP literal (incl. 0.0.0.0 / ::)
	}
	ifi, err := net.InterfaceByName(h)
	if err != nil {
		return nil, fmt.Errorf("%q is neither an IP address nor a known interface", h)
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, fmt.Errorf("interface %q: %v", h, err)
	}
	var ips []string
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		ips = append(ips, ip.String())
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("interface %q has no usable (non-loopback, non-link-local) IP address", h)
	}
	return ips, nil
}

// splitHostsOrDefault splits a comma-separated host string into entries,
// defaulting to ["127.0.0.1"] when empty.
func splitHostsOrDefault(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"127.0.0.1"}
	}
	return out
}

// resolveListenAddrs expands host entries (IPs or interface names) into deduped
// host:port listen addresses.
func resolveListenAddrs(hosts []string, port int) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, h := range hosts {
		ips, err := hostToIPs(h)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			a := net.JoinHostPort(ip, strconv.Itoa(port))
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no listen address resolved")
	}
	return out, nil
}

// arpEnabled reports whether ARP scanning is on (exposed via /api/profile).
var arpEnabled bool

// configTokens holds tokens loaded from the config file (merged with flags + config).
var configTokens []string

// serverName identifies this instance. It defaults to the OS hostname and can be
// overridden with --name. It is exposed via GET /api/ping.
var serverName = defaultServerName()

func defaultServerName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "pingmon"
}

func init() {
	flag.Var(&tokenFlags, "token", "Bearer token (lowercase uuid4) authorizing API access; repeatable. If any token is set, the API requires Authorization: Bearer <token>")
	flag.Var(&hostFlags, "host", "Host/IP or interface name to listen on; repeatable up to 3 (default 127.0.0.1; 0.0.0.0 = all). An interface name binds to its IP(s)")
	flag.Usage = printUsage
}

// resolveTokens combines --token flags with tokens from the config file and
// returns the deduped set.
func resolveTokens() []string {
	combined := append([]string{}, tokenFlags...)
	combined = append(combined, configTokens...)
	return dedupeTokens(combined)
}

// Check if running as root (required for ICMP)
func checkRootPermissions() bool {
	if runtime.GOOS == "windows" {
		// On Windows, ping doesn't require admin privileges
		return true
	}

	// On Unix systems, check for root user
	currentUser, err := user.Current()
	if err != nil {
		log.Printf("Error checking user: %v", err)
		return false
	}

	return currentUser.Uid == "0" // root has UID 0
}

// runServer parses the global flags and runs the HTTP/monitoring server. It is
// the default action when no subcommand is given.
func runServer() {
	// Parse command-line flags
	flag.Parse()

	// Load /etc/pingmon.conf (or --config). Explicit command-line flags win over
	// config values, which win over built-in defaults. Invalid config is fatal.
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	cfg, cfgFound, cfgErrs := loadConfig(*configFlag)
	if len(cfgErrs) > 0 {
		for _, e := range cfgErrs {
			log.Printf("[CONFIG] %v", e)
		}
		log.Fatalf("ERROR: %d problem(s) in %s (run: pingmon config test %s)", len(cfgErrs), *configFlag, *configFlag)
	}
	applyConfigToFlags(cfg, setFlags)
	configTokens = cfg.Tokens

	// Resolve the instance name (flag overrides the hostname default).
	if *nameFlag != "" {
		serverName = *nameFlag
	}

	// Enable debug logging across packages when requested.
	debugEnabled = *debugFlag
	pinger.SetDebug(*debugFlag)

	// Read-only mode (freeze writes / demo).
	readOnlyEnabled = *readonlyFlag

	// Enforced minimum ping interval (flag/config; default 500ms). The config
	// file value, if any, has already been applied to *minPingFlag above.
	if *minPingFlag > 0 {
		if ms := int(*minPingFlag / time.Millisecond); ms > 0 {
			minPingIntervalMs = ms
		}
	}

	// Check for root permissions
	if !checkRootPermissions() {
		log.Fatalf("ERROR: This application requires root/administrator privileges to use ICMP ping.\n" +
			"Please restart the application with sudo or as an administrator.")
	}

	// Resolve the data folder and database path; create the folder if missing.
	dataFolder, err := filepath.Abs(*dataFolderFlag)
	if err != nil {
		log.Fatalf("ERROR: invalid data folder %q: %v", *dataFolderFlag, err)
	}
	if err := os.MkdirAll(dataFolder, 0o755); err != nil {
		log.Fatalf("ERROR: could not create data folder %q: %v", dataFolder, err)
	}
	dbPath := resolveDBPath(dataFolder)

	// Open the persistence store
	st, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("ERROR: could not open database %q: %v", dbPath, err)
	}
	defer st.Close()

	// Create router
	r := mux.NewRouter()

	// Create pinger manager and resume previously monitored hosts
	pm := NewPingerManager(st)
	if err := pm.LoadHosts(); err != nil {
		log.Printf("WARNING: could not load persisted hosts: %v", err)
	}

	// Register all API routes
	registerAPIRoutes(r, pm)

	// Start the background rollup/prune job.
	rl := rollup.New(st, *rollupFlag, *rawRetainFlag, *debugFlag)
	rl.Start()

	// Optionally start ARP scanning and expose it at GET /api/arp.
	arpEnabled = *arpscanFlag
	var arpScanner *arp.Scanner
	var arpInterval, arpActiveInterval time.Duration
	if arpEnabled {
		arpInterval = *arpIntervalF // flag/config; 0 means use the default below
		if arpInterval <= 0 {
			arpInterval = 2 * time.Minute
		}
		arpActiveInterval = *arpActiveIntervalF
		if arpActiveInterval <= 0 {
			arpActiveInterval = 10 * time.Minute
		}
		arpScanner = arp.New(arpInterval)
		arpScanner.SetActive(*arpActiveFlag)
		arpScanner.SetActiveInterval(arpActiveInterval)
		arpScanner.SetActiveMaxHosts(*arpActiveMaxF)
		arpScanner.Start()
		r.HandleFunc("/api/arp", arpHandler(arpScanner.Entries)).Methods(http.MethodGet)
	}

	// This is an API-only server; the frontend is served separately. No root
	// route is registered, so "/" and any other unmapped path return 404 (the
	// service does not advertise itself to scanners). Liveness/identity are
	// available only under /api/ping and /api/profile.

	// Resolve API auth tokens (flags + config file).
	tokens := resolveTokens()
	authEnabled = len(tokens) > 0

	// Middleware chain: logging (outermost) -> CORS (fully open, handles
	// preflight) -> auth -> read-only -> router, so preflight/unauthorized/locked
	// responses are still logged and carry CORS headers.
	handler := loggingMiddleware(corsMiddleware(authMiddleware(tokens)(readonlyMiddleware(r))))

	// Resolve listen addresses (flag/config; default localhost). Each entry is an
	// IP literal or an interface name expanded to its IP(s).
	hosts := []string(hostFlags)
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1"}
	}
	if len(hosts) > maxListenHosts {
		log.Fatalf("ERROR: at most %d --host values allowed, got %d", maxListenHosts, len(hosts))
	}
	addrs, err := resolveListenAddrs(hosts, *portFlag)
	if err != nil {
		log.Fatalf("ERROR: %v", err)
	}

	srv := &http.Server{Handler: handler}

	// Bind every address up front so bind errors are fatal before we serve.
	listeners := make([]net.Listener, 0, len(addrs))
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			log.Fatalf("ERROR: cannot listen on %s: %v", a, err)
		}
		listeners = append(listeners, ln)
	}

	// Graceful shutdown: stop pingers and flush the store on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		pm.StopAll()
		rl.Stop()
		if arpScanner != nil {
			arpScanner.Stop()
		}
		if err := st.Close(); err != nil {
			log.Printf("error closing store: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("error during shutdown: %v", err)
		}
	}()

	// Colorized at-a-glance overview.
	printStartupOverview(startupInfo{
		name:             serverName,
		version:          appVersion,
		listenAddrs:      addrs,
		dataFolder:       dataFolder,
		dbPath:           dbPath,
		hostCount:        pm.Count(),
		minIntervalMs:    minPingIntervalMs,
		configPath:       configPathIf(cfgFound, *configFlag),
		tokens:           tokens,
		rawRetain:        *rawRetainFlag,
		rollupInterval:   *rollupFlag,
		debug:            debugEnabled,
		readOnly:         readOnlyEnabled,
		arpScan:          arpEnabled,
		arpInterval:      arpInterval,
		arpActive:        arpEnabled && *arpActiveFlag,
		arpActiveEvery:   arpActiveInterval,
		arpActiveMaxHost: *arpActiveMaxF,
	})

	// Serve every listener with the same server; Shutdown() closes them all.
	var wg sync.WaitGroup
	for _, ln := range listeners {
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("serve %s: %v", ln.Addr(), err)
			}
		}(ln)
	}
	wg.Wait()
}
