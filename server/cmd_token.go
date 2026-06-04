package main

import (
	"crypto/rand"
	"fmt"
	"os"

	"github.com/fatih/color"
)

func init() {
	registerCommand(command{
		name:    "token",
		summary: "Generate a random API token (lowercase uuid4); prints the token only",
		run:     runTokenCmd,
	})
}

func runTokenCmd(_ []string) int {
	tok, err := generateToken()
	if err != nil {
		fmt.Fprintf(color.Error, "token: %v\n", err)
		return 1
	}
	// Output only — pipe-friendly, e.g. sudo pingmon --token "$(pingmon token)".
	fmt.Fprintln(os.Stdout, tok)
	return 0
}

// generateToken returns a cryptographically random lowercase RFC 4122 v4 UUID
// (the format accepted by --token / config token=).
func generateToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
