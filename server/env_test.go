package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/7c/pingmon/classes/arp"
)

func TestParseKVFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pingmon.conf")
	content := "# comment\n\nTOKEN=a\ntoken = b , c\narp_interval = 30s\nOTHER=\"quoted\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	kv, err := parseKVFile(path)
	if err != nil {
		t.Fatalf("parseKVFile: %v", err)
	}
	if len(kv["token"]) != 2 { // two token= lines (lowercased key)
		t.Fatalf("expected 2 token entries, got %v", kv["token"])
	}
	if kv["other"][0] != "quoted" {
		t.Fatalf("quotes not stripped: %v", kv["other"])
	}
	if kv["arp_interval"][0] != "30s" {
		t.Fatalf("value not parsed: %v", kv["arp_interval"])
	}
	// Missing file -> (nil, nil).
	if m, err := parseKVFile(filepath.Join(dir, "absent")); err != nil || m != nil {
		t.Fatalf("missing file should be (nil,nil), got (%v,%v)", m, err)
	}
}

func TestArpHandler(t *testing.T) {
	now := time.Now().UTC()
	provider := func() []arp.Entry {
		return []arp.Entry{
			{IP: "10.0.0.1", MAC: "aa:bb:cc:dd:ee:ff", Names: []string{"gw.local"}, Interface: "en0", FirstSeen: now, LastSeen: now, Count: 3},
		}
	}
	h := arpHandler(provider)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/arp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var body struct {
		Count   int         `json:"count"`
		Entries []arp.Entry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Entries) != 1 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	e := body.Entries[0]
	if e.IP != "10.0.0.1" || e.MAC != "aa:bb:cc:dd:ee:ff" || e.Count != 3 || len(e.Names) != 1 || e.Names[0] != "gw.local" {
		t.Fatalf("entry mismatch: %+v", e)
	}
}

func TestArpCommandRegistered(t *testing.T) {
	if _, ok := findCommand("arp"); !ok {
		t.Fatalf("arp command not registered")
	}
}
