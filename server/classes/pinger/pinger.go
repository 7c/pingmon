// Package ping provides a simple way to send ICMP echo requests (pings) to a single host.
package pinger

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// Constants for ICMP packet
const (
	icmpEchoRequest = 8
	icmpEchoReply   = 0
	icmpProtocol    = "ip4:icmp"

	// UnlimitedCount represents continuous pinging
	UnlimitedCount = -1

	// Default values
	DefaultCount    = 3
	DefaultSize     = 56
	DefaultTimeout  = 2 * time.Second
	DefaultInterval = 1 * time.Second

	// Maximum number of entries to store
	maxStoredErrors  = 1200
	maxStoredRTT     = 1200
	maxStoredResults = 1200 // Maximum number of results to store
)

// Common errors
var (
	ErrInvalidTimeout  = errors.New("timeout must be greater than zero")
	ErrInvalidCount    = errors.New("count must be greater than zero or UnlimitedCount")
	ErrInvalidSize     = errors.New("size must be greater than zero")
	ErrInvalidInterval = errors.New("interval must be greater than zero")
	ErrHostNotResolved = errors.New("could not resolve host")
	ErrPermission      = errors.New("operation not permitted - this may require elevated privileges (try running as root/administrator)")
)

// PingResult contains the result of a single ping operation
type PingResult struct {
	Host    string
	IPAddr  *net.IPAddr
	RTT     time.Duration
	Success bool
	Error   error
	Seq     int // Sequence number of this ping
}

// Stats contains statistics for ping results
type Stats struct {
	Host        string
	IPAddr      *net.IPAddr
	StartTime   time.Time // When pinging started
	LastUpdate  time.Time // Time of last stats update
	Sent        int       // Number of pings sent
	Received    int       // Number of successful pings
	Loss        float64   // Packet loss percentage
	MinRTT      time.Duration
	MaxRTT      time.Duration
	AvgRTT      time.Duration
	StdDevRTT   time.Duration
	SuccessRTT  []time.Duration // RTTs of successful pings (capped at maxStoredRTT)
	ErrorsCount int             // Number of errors
	Errors      []error         // List of errors (capped at maxStoredErrors)
	Running     bool            // Whether pinging is currently active
}

// PingerOptions contains configuration options for a Pinger
type PingerOptions struct {
	Timeout  time.Duration // Timeout for each ping
	Count    int           // Number of pings to send, use UnlimitedCount for continuous
	Size     int           // Size of ping payload in bytes
	Interval time.Duration // Interval between pings
}

// DefaultPingerOptions returns the default options for a Pinger
func DefaultPingerOptions() PingerOptions {
	return PingerOptions{
		Timeout:  DefaultTimeout,
		Count:    DefaultCount,
		Size:     DefaultSize,
		Interval: DefaultInterval,
	}
}

// pinger handles ping operations to a single host
type Pinger struct {
	host     string
	ipAddr   *net.IPAddr
	Timeout  time.Duration
	Count    int
	Size     int
	Interval time.Duration

	// Internal state - all protected by mutex
	mutex     sync.RWMutex  // Protects all internal state
	id        int           // ICMP identifier
	seq       int           // Current sequence number
	startTime time.Time     // When pinging started
	results   []PingResult  // All ping results
	done      chan struct{} // Signal to stop pinging
	running   bool          // Whether pinger is currently running
}

// NewPinger creates a new Pinger for a single host with default options
func NewPinger(host string) (*Pinger, error) {
	return NewPingerWithOptions(host, DefaultPingerOptions())
}

// NewPingerWithOptions creates a new Pinger with custom options
func NewPingerWithOptions(host string, options PingerOptions) (*Pinger, error) {
	// Validate options
	if options.Timeout <= 0 {
		return nil, ErrInvalidTimeout
	}
	if options.Count == 0 || options.Count < UnlimitedCount {
		return nil, ErrInvalidCount
	}
	if options.Size <= 0 {
		return nil, ErrInvalidSize
	}
	if options.Interval <= 0 {
		return nil, ErrInvalidInterval
	}

	// Resolve the IP address
	ipAddr, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return nil, fmt.Errorf("%w: %s - %v", ErrHostNotResolved, host, err)
	}

	// Generate a random identifier
	rand.Seed(time.Now().UnixNano())
	id := rand.Intn(65535)

	// Create a new pinger
	p := &Pinger{
		host:     host,
		ipAddr:   ipAddr,
		Timeout:  options.Timeout,
		Count:    options.Count,
		Size:     options.Size,
		Interval: options.Interval,
		id:       id,
		seq:      0,
		done:     make(chan struct{}),
		results:  make([]PingResult, 0),
		running:  false,
	}

	return p, nil
}

// calculateChecksum calculates the Internet Checksum for the ICMP packet
func calculateChecksum(data []byte) uint16 {
	var sum uint32

	// Sum all the 16-bit words
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}

	// Add the last byte if the length is odd
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}

	// Add carries
	for sum > 0xFFFF {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}

	// Take the ones complement
	return ^uint16(sum)
}

// createICMPPacket creates an ICMP echo request packet
func createICMPPacket(id, seq int, size int) []byte {
	// ICMP header is 8 bytes: type, code, checksum, id, sequence
	icmp := make([]byte, 8+size)

	// Set type and code
	icmp[0] = icmpEchoRequest // Type: Echo request
	icmp[1] = 0               // Code: 0

	// ID and Sequence (2 bytes each)
	icmp[4] = byte(id >> 8)
	icmp[5] = byte(id & 0xFF)
	icmp[6] = byte(seq >> 8)
	icmp[7] = byte(seq & 0xFF)

	// Add payload to reach the desired size
	for i := 8; i < len(icmp); i++ {
		icmp[i] = byte(i)
	}

	// Calculate checksum (bytes 2 and 3)
	checksum := calculateChecksum(icmp)
	icmp[2] = byte(checksum >> 8)
	icmp[3] = byte(checksum & 0xFF)

	return icmp
}

// sendPing sends a single ping and waits for response
func (p *Pinger) sendPing() PingResult {
	p.mutex.RLock()
	host := p.host
	ipAddr := p.ipAddr
	id := p.id
	seq := p.seq
	size := p.Size
	timeout := p.Timeout
	p.mutex.RUnlock()

	log.Printf("[PING] Sending ping #%d to %s (%s) with timeout %v", seq, host, ipAddr, timeout)

	result := PingResult{
		Host:    host,
		IPAddr:  ipAddr,
		Success: false,
		Seq:     seq,
	}

	// Create a connection
	conn, err := net.DialIP(icmpProtocol, nil, ipAddr)
	if err != nil {
		if opErr, ok := err.(*net.OpError); ok && opErr.Err != nil {
			if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
				if syscallErr.Err == syscall.EPERM || syscallErr.Err == syscall.EACCES {
					// Permission error - need elevated privileges
					result.Error = ErrPermission
					return result
				}
			}
		}
		result.Error = fmt.Errorf("could not create connection: %v", err)
		return result
	}
	defer conn.Close()

	// Create ICMP packet
	icmpPacket := createICMPPacket(id, seq, size)

	// Set the read deadline
	conn.SetReadDeadline(time.Now().Add(timeout))

	// Send the packet
	startTime := time.Now()
	if _, err := conn.Write(icmpPacket); err != nil {
		result.Error = fmt.Errorf("could not send ICMP packet: %v", err)
		return result
	}

	// Read the reply
	reply := make([]byte, 1500) // MTU size buffer
	n, err := conn.Read(reply)
	if err != nil {
		result.Error = fmt.Errorf("error reading response: %v", err)
		return result
	}

	// Calculate RTT - ensuring we're using nanosecond precision
	endTime := time.Now()
	result.RTT = endTime.Sub(startTime)

	// Validate the reply
	if n < 20+8 { // 20 bytes for IP header and 8 bytes for ICMP header
		result.Error = errors.New("received packet too short")
		return result
	}

	// Skip the IP header (typically 20 bytes)
	icmpReply := reply[20:]

	// Check ICMP type and code
	if icmpReply[0] != icmpEchoReply || icmpReply[1] != 0 {
		result.Error = fmt.Errorf("received non-echo reply type=%d, code=%d", icmpReply[0], icmpReply[1])
		return result
	}

	// Check ID and sequence match
	replyID := int(icmpReply[4])<<8 | int(icmpReply[5])
	replySeq := int(icmpReply[6])<<8 | int(icmpReply[7])

	if replyID != id || replySeq != seq {
		result.Error = fmt.Errorf("received reply with wrong ID or sequence: expected id=%d, seq=%d but got id=%d, seq=%d",
			id, seq, replyID, replySeq)
		return result
	}

	// Ping successful
	result.Success = true
	return result
}

// addResult safely adds a result to the results slice
func (p *Pinger) addResult(result PingResult) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Add new result
	p.results = append(p.results, result)

	// If we exceed maxStoredResults, keep only the most recent ones
	if len(p.results) > maxStoredResults {
		p.results = p.results[len(p.results)-maxStoredResults:]
	}

	p.seq++
}

// IsRunning returns whether the pinger is currently running
func (p *Pinger) IsRunning() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.running
}

// pingLoop is the internal method that runs in a goroutine
func (p *Pinger) pingLoop() {
	defer func() {
		p.mutex.Lock()
		p.running = false
		p.mutex.Unlock()
	}()

	for {
		// Check if we should stop
		select {
		case <-p.done:
			return
		default:
			// Continue
		}

		// Check if we've reached the count limit (if not unlimited)
		p.mutex.RLock()
		count := p.Count
		seq := p.seq
		interval := p.Interval
		shouldStop := count != UnlimitedCount && seq >= count
		p.mutex.RUnlock()

		if shouldStop {
			return
		}

		// Send a ping
		result := p.sendPing()

		// Log the result
		if result.Success {
			log.Printf("[PING] #%d to %s successful - RTT: %v", result.Seq, result.Host, result.RTT)
		} else {
			log.Printf("[PING] #%d to %s failed - Error: %v", result.Seq, result.Host, result.Error)
		}

		// Add the result
		p.addResult(result)

		// Wait the interval before next ping
		select {
		case <-p.done:
			return
		case <-time.After(interval):
			// Continue to next ping
		}
	}
}

// Run starts the ping operations in a goroutine
func (p *Pinger) Run() {
	p.mutex.Lock()

	// Check if already running
	if p.running {
		p.mutex.Unlock()
		log.Printf("[PING] Pinger for %s is already running, ignoring Run() call", p.host)
		return
	}

	// Set running state
	p.running = true

	// Reset sequence number and start time
	p.seq = 0
	p.startTime = time.Now()

	// Clear previous results
	p.results = make([]PingResult, 0)

	// Create new done channel
	p.done = make(chan struct{})

	log.Printf("[PING] Starting pinger for %s (%s) with interval %v, timeout %v, count %d",
		p.host, p.ipAddr, p.Interval, p.Timeout, p.Count)

	// Store these values so we can unlock before starting goroutine
	p.mutex.Unlock()

	// Start the ping loop in a goroutine
	go p.pingLoop()
}

// Stop stops the ping operations
func (p *Pinger) Stop() {
	p.mutex.Lock()

	// Only stop if running
	if p.running {
		log.Printf("[PING] Stopping pinger for %s", p.host)
		close(p.done)
	} else {
		log.Printf("[PING] Pinger for %s is not running, ignoring Stop() call", p.host)
	}

	p.mutex.Unlock()
}

// GetResults returns all ping results
func (p *Pinger) GetResults() []PingResult {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	// Make a copy to avoid race conditions
	resultsCopy := make([]PingResult, len(p.results))
	copy(resultsCopy, p.results)

	return resultsCopy
}

// GetStatistics returns current statistics and clears accumulated results
func (p *Pinger) GetStatistics() Stats {
	// Copy state and results, then clear
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	stats := Stats{
		Host:       p.host,
		IPAddr:     p.ipAddr,
		StartTime:  p.startTime,
		LastUpdate: time.Now(),
		Running:    p.running,
		SuccessRTT: make([]time.Duration, 0, maxStoredRTT),
		Errors:     make([]error, 0, maxStoredErrors),
	}

	// Process each result
	successCount := 0
	errorCount := 0

	for _, result := range p.results {
		stats.Sent++

		if result.Success {
			stats.Received++
			successCount++

			// Only add to SuccessRTT if we haven't reached the limit
			if successCount <= maxStoredRTT {
				stats.SuccessRTT = append(stats.SuccessRTT, result.RTT)
			} else if successCount == maxStoredRTT+1 {
				// When we exceed the limit, we need to adjust arrays for proper averaging
				// We'll keep most recent maxStoredRTT results
				copy(stats.SuccessRTT, stats.SuccessRTT[1:])
				stats.SuccessRTT[maxStoredRTT-1] = result.RTT
			}
		} else if result.Error != nil {
			errorCount++
			stats.ErrorsCount++
			// Add to errors list, but cap at maxStoredErrors
			if errorCount <= maxStoredErrors {
				stats.Errors = append(stats.Errors, result.Error)
			} else if errorCount == maxStoredErrors+1 {
				// Remove oldest error (first one) and add new one at the end
				copy(stats.Errors, stats.Errors[1:])
				stats.Errors[maxStoredErrors-1] = result.Error
			}
		}
	}

	// Calculate packet loss
	packetLoss := 0.0
	if stats.Sent > 0 {
		packetLoss = float64(stats.Sent-stats.Received) / float64(stats.Sent)
	}
	stats.Loss = packetLoss

	// Log the statistics
	log.Printf("[PING-STATS] %s: sent=%d received=%d loss=%.2f%% min=%v avg=%v max=%v",
		p.host, stats.Sent, stats.Received, packetLoss*100, stats.MinRTT, stats.AvgRTT, stats.MaxRTT)

	// Calculate RTT statistics based on stored RTTs (which may be capped)
	if len(stats.SuccessRTT) > 0 {
		stats.MinRTT = stats.SuccessRTT[0]
		stats.MaxRTT = stats.SuccessRTT[0]
		var sum time.Duration

		for _, rtt := range stats.SuccessRTT {
			if rtt < stats.MinRTT {
				stats.MinRTT = rtt
			}
			if rtt > stats.MaxRTT {
				stats.MaxRTT = rtt
			}
			sum += rtt
		}

		stats.AvgRTT = sum / time.Duration(len(stats.SuccessRTT))

		// Calculate standard deviation
		if len(stats.SuccessRTT) > 1 {
			var sumSquares float64
			for _, rtt := range stats.SuccessRTT {
				diff := float64(rtt - stats.AvgRTT)
				sumSquares += diff * diff
			}
			variance := sumSquares / float64(len(stats.SuccessRTT))
			stats.StdDevRTT = time.Duration(int64(variance))
		}
	}

	return stats
}
