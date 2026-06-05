package main

import (
	"os"
	"strings"
	"testing"
)

func TestAppVersionMatchesOpenAPI(t *testing.T) {
	data, err := os.ReadFile("openapi.yml")
	if err != nil {
		t.Fatalf("read openapi.yml: %v", err)
	}
	want := "version: " + appVersion
	if !strings.Contains(string(data), want) {
		t.Fatalf("openapi.yml info.version out of sync with appVersion %q", appVersion)
	}
}

func TestMultiHostSet(t *testing.T) {
	var m multiHost
	if err := m.Set("1.2.3.4"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Comma-separated and repeated values both accumulate; blanks are skipped.
	if err := m.Set(" 5.6.7.8 , , eth0 "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := []string(m)
	want := []string{"1.2.3.4", "5.6.7.8", "eth0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if s := m.String(); s != "1.2.3.4,5.6.7.8,eth0" {
		t.Fatalf("String: %q", s)
	}
}

func TestHostToIPs(t *testing.T) {
	// An IP literal maps to itself.
	ips, err := hostToIPs("10.0.0.1")
	if err != nil {
		t.Fatalf("hostToIPs(ip): %v", err)
	}
	if len(ips) != 1 || ips[0] != "10.0.0.1" {
		t.Fatalf("ip literal: got %v", ips)
	}

	// An unknown interface name is a clear error.
	if _, err := hostToIPs("definitely-not-an-iface-xyz"); err == nil {
		t.Fatalf("expected error for unknown interface")
	} else if !strings.Contains(err.Error(), "neither an IP address nor a known interface") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveListenAddrs(t *testing.T) {
	// Dedups identical IPs and joins host:port.
	addrs, err := resolveListenAddrs([]string{"127.0.0.1", "127.0.0.1", "0.0.0.0"}, 6868)
	if err != nil {
		t.Fatalf("resolveListenAddrs: %v", err)
	}
	want := []string{"127.0.0.1:6868", "0.0.0.0:6868"}
	if len(addrs) != len(want) {
		t.Fatalf("got %v, want %v", addrs, want)
	}
	for i := range want {
		if addrs[i] != want[i] {
			t.Fatalf("got %v, want %v", addrs, want)
		}
	}

	// A bad host aborts resolution.
	if _, err := resolveListenAddrs([]string{"definitely-not-an-iface-xyz"}, 6868); err == nil {
		t.Fatalf("expected error for unknown host")
	}
}

func TestSplitHostsOrDefault(t *testing.T) {
	if got := splitHostsOrDefault(""); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("empty: got %v", got)
	}
	if got := splitHostsOrDefault("  "); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("blank: got %v", got)
	}
	got := splitHostsOrDefault("1.1.1.1, eth0 ,2.2.2.2")
	want := []string{"1.1.1.1", "eth0", "2.2.2.2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
