// Package oplog appends Mole's Windows operation log at
// %LOCALAPPDATA%\mole\logs\operations.log. Every destructive Windows command
// records what it deleted, moved, or kept there. MO_NO_OPLOG=1 disables it,
// and logging problems never block the operation being logged.
package oplog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Log is an open operation log; the zero value discards writes.
type Log struct {
	command string
	w       *bufio.Writer
	f       *os.File
}

// Now is replaceable in tests.
var Now = time.Now

// Open starts logging for command (for example "clean" or "uninstall").
func Open(command string) *Log {
	l := &Log{command: command}
	local := os.Getenv("LOCALAPPDATA")
	if os.Getenv("MO_NO_OPLOG") == "1" || local == "" {
		return l
	}
	dir := filepath.Join(local, "mole", "logs")
	if os.MkdirAll(dir, 0o755) != nil {
		return l
	}
	f, err := os.OpenFile(filepath.Join(dir, "operations.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return l
	}
	l.w, l.f = bufio.NewWriter(f), f
	return l
}

// Write records one action ("deleted", "kept", "trashed", "ran", ...).
func (l *Log) Write(action, path string, size int64) {
	if l == nil || l.w == nil {
		return
	}
	_, _ = fmt.Fprintf(l.w, "%s\t%s\t%s\t%d\t%s\n", Now().UTC().Format(time.RFC3339), l.command, action, size, path)
}

// Close flushes and closes the log.
func (l *Log) Close() {
	if l == nil || l.w == nil {
		return
	}
	_ = l.w.Flush()
	_ = l.f.Close()
}
