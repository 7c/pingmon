package arp

import "testing"

func TestParseProcNetARP(t *testing.T) {
	data := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.1      0x1         0x2         ab:cd:ef:12:34:56     *        eth0
192.168.1.50     0x1         0x0         00:00:00:00:00:00     *        eth0
10.0.0.5         0x1         0x2         a:b:c:1:2:3           *        wlan0
`
	got := parseProcNetARP(data)
	if len(got) != 2 {
		t.Fatalf("expected 2 usable entries (incomplete skipped), got %d: %+v", len(got), got)
	}
	if got[0].ip != "192.168.1.1" || got[0].mac != "ab:cd:ef:12:34:56" || got[0].iface != "eth0" {
		t.Errorf("entry0 wrong: %+v", got[0])
	}
	// Zero-padding normalization.
	if got[1].mac != "0a:0b:0c:01:02:03" {
		t.Errorf("expected normalized mac, got %q", got[1].mac)
	}
}

func TestParseArpCommand(t *testing.T) {
	out := `? (192.168.1.1) at ab:cd:ef:12:34:56 on en0 ifscope [ethernet]
? (192.168.1.255) at ff:ff:ff:ff:ff:ff on en0 ifscope permanent [ethernet]
? (192.168.1.77) at (incomplete) on en0 ifscope [ethernet]
? (10.0.0.9) at a:1:b:2:c:3 on en1 [ethernet]
`
	got := parseArpCommand(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 usable entries (broadcast+incomplete skipped), got %d: %+v", len(got), got)
	}
	if got[0].ip != "192.168.1.1" || got[0].iface != "en0" {
		t.Errorf("entry0 wrong: %+v", got[0])
	}
	if got[1].ip != "10.0.0.9" || got[1].mac != "0a:01:0b:02:0c:03" || got[1].iface != "en1" {
		t.Errorf("entry1 wrong: %+v", got[1])
	}
}

func TestAccumulateMerge(t *testing.T) {
	s := New(0)
	s.SetResolve(false)

	// Simulate three scans by merging raw entries directly.
	s.merge([]rawEntry{{ip: "10.0.0.1", mac: "aa:bb:cc:dd:ee:ff", iface: "en0"}}, nil)
	s.merge([]rawEntry{{ip: "10.0.0.1", mac: "aa:bb:cc:dd:ee:ff", iface: "en0"}}, nil)
	// Host changed its MAC; we keep firstSeen, bump count, take latest MAC.
	s.merge([]rawEntry{
		{ip: "10.0.0.1", mac: "11:22:33:44:55:66", iface: "en1"},
		{ip: "10.0.0.2", mac: "de:ad:be:ef:00:01", iface: "en0"},
	}, nil)

	all := s.Entries()
	if len(all) != 2 {
		t.Fatalf("expected 2 accumulated entries, got %d", len(all))
	}
	e := all[0] // 10.0.0.1
	if e.IP != "10.0.0.1" {
		t.Fatalf("unexpected first entry %+v", e)
	}
	if e.Count != 3 {
		t.Errorf("expected count 3, got %d", e.Count)
	}
	if e.MAC != "11:22:33:44:55:66" || e.Interface != "en1" {
		t.Errorf("expected latest mac/iface, got %s/%s", e.MAC, e.Interface)
	}
	if e.FirstSeen.IsZero() || e.LastSeen.Before(e.FirstSeen) {
		t.Errorf("firstSeen/lastSeen wrong: first=%v last=%v", e.FirstSeen, e.LastSeen)
	}
	if all[1].Count != 1 {
		t.Errorf("second host should have count 1, got %d", all[1].Count)
	}
}

func TestEntriesSortedByIP(t *testing.T) {
	s := New(0)
	s.SetResolve(false)
	s.entries = map[string]Entry{
		"192.168.1.20": {IP: "192.168.1.20"},
		"192.168.1.3":  {IP: "192.168.1.3"},
		"10.0.0.1":     {IP: "10.0.0.1"},
	}
	got := s.Entries()
	want := []string{"10.0.0.1", "192.168.1.3", "192.168.1.20"}
	for i, w := range want {
		if got[i].IP != w {
			t.Fatalf("sort order wrong at %d: got %s want %s", i, got[i].IP, w)
		}
	}
}
