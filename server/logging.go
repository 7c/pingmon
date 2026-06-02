package main

import (
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

// Log colors. fatih/color auto-disables these when output is not a terminal or
// NO_COLOR is set.
var (
	logDim  = color.New(color.Faint)
	logTime = color.New(color.Faint)
	logErr  = color.New(color.FgRed, color.Bold)
	logWarn = color.New(color.FgYellow, color.Bold)
)

// tagColors maps a leading "[TAG]" log prefix to a color.
var tagColors = map[string]*color.Color{
	"[DEBUG]":      color.New(color.Faint),
	"[PING]":       color.New(color.FgCyan),
	"[PING-STATS]": color.New(color.FgCyan),
	"[STORE]":      color.New(color.FgMagenta),
	"[API]":        color.New(color.FgMagenta),
	"[ROLLUP]":     color.New(color.FgBlue),
	"[STARTUP]":    color.New(color.FgGreen),
	"[RESET]":      color.New(color.FgYellow),
	"[HTTP]":       color.New(color.Faint),
}

// setupLogging routes the standard logger through a colorized, timestamped
// writer so every log line across packages gets a consistent format.
func setupLogging() {
	log.SetFlags(0) // we add our own timestamp in the writer
	log.SetOutput(&colorLogWriter{out: color.Error})
}

// colorLogWriter prepends a dim timestamp and colorizes the line before writing.
type colorLogWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *colorLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	line := strings.TrimRight(string(p), "\n")
	ts := logTime.Sprint(time.Now().Format("15:04:05.000"))
	if _, err := io.WriteString(w.out, ts+" "+colorizeLogLine(line)+"\n"); err != nil {
		return 0, err
	}
	return len(p), nil
}

// colorizeLogLine colors a leading [TAG] prefix, and whole-line ERROR/WARNING
// messages.
func colorizeLogLine(line string) string {
	switch {
	case strings.HasPrefix(line, "ERROR"):
		return logErr.Sprint(line)
	case strings.HasPrefix(line, "WARNING"):
		return logWarn.Sprint(line)
	}
	if strings.HasPrefix(line, "[") {
		if i := strings.IndexByte(line, ']'); i > 0 {
			tag := line[:i+1]
			if c, ok := tagColors[tag]; ok {
				return c.Sprint(tag) + line[i+1:]
			}
		}
	}
	return line
}

// statusColor returns a color for an HTTP status code by class.
func statusColor(code int) *color.Color {
	switch {
	case code >= 500:
		return color.New(color.FgRed, color.Bold)
	case code >= 400:
		return color.New(color.FgYellow)
	case code >= 300:
		return color.New(color.FgCyan)
	default:
		return color.New(color.FgGreen)
	}
}

// debugEnabled gates verbose [DEBUG] logging across the main package. Set from
// the --debug flag in main().
var debugEnabled bool

// debugf logs a [DEBUG]-prefixed line only when debug logging is enabled.
func debugf(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf("[DEBUG] "+format, args...)
	}
}
