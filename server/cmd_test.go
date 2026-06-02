package main

import (
	"strconv"
	"testing"
)

func TestCommandsRegistered(t *testing.T) {
	for _, name := range []string{"arp", "stats", "host", "group"} {
		if _, ok := findCommand(name); !ok {
			t.Errorf("command %q not registered", name)
		}
	}
}

// TestCliStoreCommands drives the store-backed CLI commands against a temp data
// folder and verifies they mutate the store (no server / no token).
func TestCliStoreCommands(t *testing.T) {
	dir := t.TempDir()
	df := []string{"--datafolder", dir}

	if rc := runGroupCmd(append([]string{"add", "--color", "#abc", "--desc", "edge nodes"}, append(df, "edge")...)); rc != 0 {
		t.Fatalf("group add rc=%d", rc)
	}
	if rc := runHostCmd(append([]string{"add", "--interval", "100", "--name", "router", "--tags", "core,lan"}, append(df, "192.168.1.1")...)); rc != 0 {
		t.Fatalf("host add rc=%d", rc)
	}
	// Note: Go's flag package stops at the first positional, so store flags
	// must precede positional args.
	if rc := runGroupCmd(append(append([]string{"assign"}, df...), "1", "192.168.1.1")); rc != 0 {
		t.Fatalf("group assign rc=%d", rc)
	}
	if rc := runStatsCmd(append([]string{"--json"}, df...)); rc != 0 {
		t.Fatalf("stats rc=%d", rc)
	}

	// Verify persisted state directly.
	st, _, err := openStoreFromFlags(dir, "pingmon.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	h, err := st.GetHost("192.168.1.1")
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if h.DisplayName != "router" || len(h.Tags) != 2 {
		t.Fatalf("host meta not applied: %+v", h)
	}
	// Interval below the min was clamped to 500.
	if h.Config.IntervalMs != minPingIntervalMs {
		t.Fatalf("expected interval clamped to %d, got %d", minPingIntervalMs, h.Config.IntervalMs)
	}
	if len(h.Groups) != 1 {
		t.Fatalf("expected host in 1 group, got %v", h.Groups)
	}

	groups, _ := st.ListGroups()
	if len(groups) != 1 || groups[0].Name != "edge" {
		t.Fatalf("unexpected groups: %+v", groups)
	}

	// group edit: rename, keeping color/desc.
	gid := groups[0].ID
	if rc := runGroupCmd([]string{"edit", "--datafolder", dir, "--name", "edge2", strconv.FormatInt(gid, 10)}); rc != 0 {
		t.Fatalf("group edit rc=%d", rc)
	}
	g, _ := st.GetGroup(gid)
	if g.Name != "edge2" || g.Color != "#abc" {
		t.Fatalf("edit clobbered fields: %+v", g)
	}
}
