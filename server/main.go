package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/7c/pingmon/classes/pinger"
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
}

// NewPingerManager creates a new pinger manager
func NewPingerManager() *PingerManager {
	return &PingerManager{
		pingers: make(map[string]*pinger.Pinger),
	}
}

// AddPinger adds a new pinger for the given IP
func (pm *PingerManager) AddPinger(ip string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Check if pinger already exists
	if _, exists := pm.pingers[ip]; exists {
		return nil // Already exists
	}

	// Create a new pinger
	options := pinger.DefaultPingerOptions()
	options.Count = pinger.UnlimitedCount // Run continuously

	p, err := pinger.NewPingerWithOptions(ip, options)
	if err != nil {
		return err
	}

	// Start the pinger
	p.Run()

	// Store the pinger
	pm.pingers[ip] = p
	return nil
}

// RemovePinger removes and stops a pinger
func (pm *PingerManager) RemovePinger(ip string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if p, exists := pm.pingers[ip]; exists {
		p.Stop()
		delete(pm.pingers, ip)
	}
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
			Stats: baseStats,
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

// ResetPingers stops and restarts specified pingers, clearing their stats
func (pm *PingerManager) ResetPingers(ips []string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, ip := range ips {
		if p, exists := pm.pingers[ip]; exists {
			// Stop the current pinger
			p.Stop()
			
			// Create a new pinger with the same options
			options := pinger.DefaultPingerOptions()
			options.Count = pinger.UnlimitedCount
			
			newPinger, err := pinger.NewPingerWithOptions(ip, options)
			if err == nil {
				// Replace with new pinger and start it
				pm.pingers[ip] = newPinger
				newPinger.Run()
			}
		}
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

// loggingMiddleware logs all HTTP requests including static file requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Log the request details
		log.Printf(
			"[%s] %s %s %s",
			time.Now().Format("2006-01-02 15:04:05"),
			r.Method,
			r.RequestURI,
			r.RemoteAddr,
		)

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
			"[%s] %s %s %s - Completed with status %d in %v",
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
var portFlag = flag.Int("port", 6868, "Port to run the server on")

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

// Check if build directory has index.html
func checkBuildDirectory() bool {
	buildDir := "build"
	indexPath := filepath.Join(buildDir, "index.html")
	
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return false
	}
	
	return true
}

func main() {
	// Parse command-line flags
	flag.Parse()
	
	// Configure logger
	log.SetFlags(0) // Remove timestamp as we're adding our own

	// Check for root permissions
	if !checkRootPermissions() {
		log.Fatalf("ERROR: This application requires root/administrator privileges to use ICMP ping.\n" +
			"Please restart the application with sudo or as an administrator.")
	}

	// Check for build directory and index.html
	if !checkBuildDirectory() {
		log.Fatalf("ERROR: The build directory is missing index.html.\n" +
			"Please run 'npm run build' in the client directory first.")
	}

	// Create router
	r := mux.NewRouter()

	// Create pinger manager
	pm := NewPingerManager()

	// API Routes
	apiRouter := r.PathPrefix("/api").Subrouter()

	// GET /api/pinger - get all pinger stats
	apiRouter.HandleFunc("/pinger", func(w http.ResponseWriter, r *http.Request) {
		stats := pm.GetStats()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}).Methods("GET")

	// POST /api/pinger/add - add new pinger(s)
	apiRouter.HandleFunc("/pinger/add", func(w http.ResponseWriter, r *http.Request) {
		var req AddPingerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		errors := make(map[string]string)
		for _, ip := range req.IPs {
			if err := pm.AddPinger(ip); err != nil {
				errors[ip] = err.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if len(errors) > 0 {
			w.WriteHeader(http.StatusPartialContent)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": len(errors) < len(req.IPs),
				"errors":  errors,
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
			})
		}
	}).Methods("POST")

	// POST /api/pinger/reset - reset pinger(s)
	apiRouter.HandleFunc("/pinger/reset", func(w http.ResponseWriter, r *http.Request) {
		var req ResetPingerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pm.ResetPingers(req.IPs)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}).Methods("POST")

	// POST /api/pinger/remove - remove pinger(s) completely
	apiRouter.HandleFunc("/pinger/remove", func(w http.ResponseWriter, r *http.Request) {
		var req RemovePingerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pm.RemovePingers(req.IPs)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}).Methods("POST")

	// Static file serving
	staticDir := "build"
	fs := http.FileServer(http.Dir(staticDir))

	// Create build directory if it doesn't exist
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		os.MkdirAll(staticDir, 0755)
	}

	// Serve static files and handle SPA routing
	r.PathPrefix("/assets/").Handler(http.StripPrefix("/", fs))
	r.PathPrefix("/vite.svg").Handler(http.StripPrefix("/", fs))

	// For any other request, serve index.html (for SPA routing)
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the path exists as a file
		filePath := filepath.Join(staticDir, r.URL.Path)
		_, err := os.Stat(filePath)
		if os.IsNotExist(err) {
			// If file doesn't exist, serve index.html for client-side routing
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		}
		// Otherwise, serve the requested file
		http.ServeFile(w, r, filePath)
	})

	// Apply logging middleware to all requests
	handler := loggingMiddleware(r)

	// Get port from command line flag or use default
	port := *portFlag
	fmt.Printf("PingMon Server is running at http://127.0.0.1:%d\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), handler))
}
