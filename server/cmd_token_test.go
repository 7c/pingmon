package main

import "testing"

func TestGenerateToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if !isValidUUID4Lower(tok) {
			t.Fatalf("generated token is not a lowercase uuid4: %q", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token generated: %q", tok)
		}
		seen[tok] = true
	}
}

func TestTokenCommandRegistered(t *testing.T) {
	if _, ok := findCommand("token"); !ok {
		t.Fatalf("token command not registered")
	}
	if rc := runTokenCmd(nil); rc != 0 {
		t.Fatalf("runTokenCmd rc=%d", rc)
	}
}
