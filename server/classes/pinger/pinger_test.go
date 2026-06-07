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

// echoReplyPacket builds a minimal IPv4+ICMP echo-reply buffer carrying the
// given id and the low 16 bits of seq, as a real reply would.
func echoReplyPacket(id, seq int) []byte {
	buf := make([]byte, 20+8)
	icmp := buf[20:]
	icmp[0] = icmpEchoReply
	icmp[1] = 0
	icmp[4] = byte(id >> 8)
	icmp[5] = byte(id & 0xFF)
	icmp[6] = byte(seq >> 8)
	icmp[7] = byte(seq & 0xFF)
	return buf
}

// TestMatchEchoReplySequenceWrap guards the bug where a host falsely reads as
// 100% down once its sequence counter passes 65535: the ICMP header only
// carries 16 bits, so matching must compare the low 16 bits of seq.
func TestMatchEchoReplySequenceWrap(t *testing.T) {
	const id = 4242

	// Below the 16-bit boundary the full value and low bits agree.
	if !matchEchoReply(echoReplyPacket(id, 100), id, 100) {
		t.Fatalf("expected match for seq=100")
	}

	// seq has wrapped past 65535: the wire carries seq&0xFFFF, but the pinger's
	// counter is larger. This must still match the corresponding reply.
	for _, seq := range []int{65536, 65537, 131072, 200000} {
		pkt := echoReplyPacket(id, seq&0xFFFF)
		if !matchEchoReply(pkt, id, seq) {
			t.Errorf("seq=%d (wire=%d): expected match after 16-bit wrap", seq, seq&0xFFFF)
		}
	}
}

func TestMatchEchoReplyRejectsOthers(t *testing.T) {
	const id, seq = 4242, 7

	if matchEchoReply(echoReplyPacket(9999, seq), id, seq) {
		t.Error("reply for a different id must not match (cross-talk between pingers)")
	}
	if matchEchoReply(echoReplyPacket(id, seq+1), id, seq) {
		t.Error("reply for a different sequence must not match")
	}
	// An echo *request* (type 8) copied onto the socket must be ignored.
	req := echoReplyPacket(id, seq)
	req[20] = icmpEchoRequest
	if matchEchoReply(req, id, seq) {
		t.Error("echo request must not be matched as a reply")
	}
	// Truncated buffers are not matches.
	if matchEchoReply(make([]byte, 10), id, seq) {
		t.Error("short packet must not match")
	}
}

func TestPingerIDsAreUnique(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		p, err := NewPinger("127.0.0.1")
		if err != nil {
			t.Fatalf("NewPinger() error: %v", err)
		}
		if seen[p.id] {
			t.Fatalf("duplicate pinger id %d on iteration %d", p.id, i)
		}
		seen[p.id] = true
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
