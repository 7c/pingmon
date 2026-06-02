// Package rollup runs a background scheduler that periodically aggregates raw
// ping results into hourly/daily rollup tables and prunes aged raw data.
package rollup

import (
	"log"
	"sync"
	"time"

	"github.com/7c/pingmon/classes/store"
)

const (
	// hourlySafety/dailySafety leave the still-filling current bucket alone.
	hourlySafety = 10 * time.Minute
	dailySafety  = 1 * time.Hour

	// pruneEvery limits how often pruning runs relative to rollup ticks.
	pruneEvery = 12
)

// Runner periodically drives the store's rollup and prune routines.
type Runner struct {
	store     *store.Store
	interval  time.Duration
	rawRetain time.Duration // 0 disables pruning
	debug     bool
	done      chan struct{}
	wg        sync.WaitGroup
}

// New creates a rollup Runner. A zero interval defaults to 5 minutes. When debug
// is true, each run logs a summary even on success.
func New(s *store.Store, interval, rawRetain time.Duration, debug bool) *Runner {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Runner{
		store:     s,
		interval:  interval,
		rawRetain: rawRetain,
		debug:     debug,
		done:      make(chan struct{}),
	}
}

// Start launches the background loop.
func (r *Runner) Start() {
	r.wg.Add(1)
	go r.loop()
}

// Stop signals the loop to exit and waits for the in-flight tick to finish.
func (r *Runner) Stop() {
	close(r.done)
	r.wg.Wait()
}

func (r *Runner) loop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Run once shortly after startup so fresh data is rolled up promptly.
	r.runOnce(0)

	tick := 0
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			tick++
			r.runOnce(tick)
		}
	}
}

// runOnce flushes pending raw writes, then rolls up and (periodically) prunes.
func (r *Runner) runOnce(tick int) {
	now := time.Now().UTC()
	start := time.Now()

	// Ensure the async writer has flushed so finalized buckets are complete.
	r.store.Sync()

	if err := r.store.RunHourlyRollup(now, hourlySafety); err != nil {
		log.Printf("[ROLLUP] hourly rollup failed: %v", err)
	}
	if err := r.store.RunDailyRollup(now, dailySafety); err != nil {
		log.Printf("[ROLLUP] daily rollup failed: %v", err)
	}

	if r.rawRetain > 0 && tick%pruneEvery == 0 {
		if n, err := r.store.PruneRawResults(now, r.rawRetain); err != nil {
			log.Printf("[ROLLUP] prune failed: %v", err)
		} else if n > 0 {
			log.Printf("[ROLLUP] pruned %d raw results older than %s", n, r.rawRetain)
		}
	}

	if r.debug {
		log.Printf("[DEBUG] [ROLLUP] tick %d completed in %v", tick, time.Since(start))
	}
}
