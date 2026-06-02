package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

// command is a CLI subcommand. The default (no subcommand) runs the server.
type command struct {
	name    string
	summary string
	run     func(args []string) int // returns the process exit code
}

// commandRegistry holds subcommands in registration order (for --help).
var commandRegistry []command

func registerCommand(c command) { commandRegistry = append(commandRegistry, c) }

func findCommand(name string) (command, bool) {
	for _, c := range commandRegistry {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// main dispatches to a subcommand, the help screen, or the server (default).
func main() {
	setupLogging()

	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			printUsage()
			return
		}
		// A non-flag first arg is treated as a subcommand.
		if !strings.HasPrefix(args[0], "-") {
			if c, ok := findCommand(args[0]); ok {
				os.Exit(c.run(args[1:]))
			}
			fmt.Fprintf(color.Error, "unknown command: %s\n\n", args[0])
			printUsage()
			os.Exit(2)
		}
	}

	// No subcommand: run the server with the global flags.
	runServer()
}
