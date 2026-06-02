package pinger

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSetDebugGatesOutput(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	defer SetDebug(false)

	SetDebug(false)
	dbgf("should-not-appear")
	if buf.Len() != 0 {
		t.Fatalf("expected no output when debug disabled, got %q", buf.String())
	}

	SetDebug(true)
	dbgf("marker-%d", 42)
	if !strings.Contains(buf.String(), "marker-42") {
		t.Fatalf("expected debug output when enabled, got %q", buf.String())
	}
}

func TestNewPingerValidatesOptions(t *testing.T) {
	cases := map[string]PingerOptions{
		"zero timeout":  {Timeout: 0, Count: 1, Size: 56, Interval: time.Second},
		"bad count":     {Timeout: time.Second, Count: -2, Size: 56, Interval: time.Second},
		"zero size":     {Timeout: time.Second, Count: 1, Size: 0, Interval: time.Second},
		"zero interval": {Timeout: time.Second, Count: 1, Size: 56, Interval: 0},
	}
	for name, opts := range cases {
		if _, err := NewPingerWithOptions("127.0.0.1", opts); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}
}

func TestNewPingerUnresolvableHost(t *testing.T) {
	if _, err := NewPinger("this.is.not.a.valid.host.invalid"); err == nil {
		t.Fatalf("expected resolution error for invalid host")
	}
}

// TestResultHandlerFires verifies that the registered handler is invoked for
// each completed ping with a populated timestamp. It does not require ICMP
// privileges: on an unprivileged host the ping fails and produces an error
// result, which still flows through the handler.
func TestResultHandlerFires(t *testing.T) {
	opts := DefaultPingerOptions()
	opts.Count = UnlimitedCount
	opts.Interval = 50 * time.Millisecond

	p, err := NewPingerWithOptions("127.0.0.1", opts)
	if err != nil {
		t.Fatalf("NewPingerWithOptions() error: %v", err)
	}

	var (
		mu      sync.Mutex
		results []PingResult
	)
	done := make(chan struct{})
	p.SetResultHandler(func(r PingResult) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
		if len(results) == 1 {
			close(done)
		}
	})

	p.Run()
	defer p.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("result handler was not invoked within 5s")
	}

	mu.Lock()
	defer mu.Unlock()
	first := results[0]
	if first.Host != "127.0.0.1" {
		t.Errorf("unexpected host %q", first.Host)
	}
	if first.Timestamp.IsZero() {
		t.Errorf("expected a non-zero timestamp on the result")
	}
}

func TestClearResultHandler(t *testing.T) {
	p, err := NewPinger("127.0.0.1")
	if err != nil {
		t.Fatalf("NewPinger() error: %v", err)
	}
	// Setting then clearing the handler must not panic.
	p.SetResultHandler(func(PingResult) {})
	p.SetResultHandler(nil)
}
