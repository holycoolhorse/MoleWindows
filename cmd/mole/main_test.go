package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRoutesMetaCommands(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args prints usage", []string{"mole"}, 0, "Usage: mole <command>", ""},
		{"help", []string{"mole", "help"}, 0, "Commands:", ""},
		{"dash help", []string{"mole", "--help"}, 0, "analyze [PATH]", ""},
		{"version", []string{"mole", "--version"}, 0, "Mole version dev", ""},
		{"asset file name is not echoed", []string{`C:\Tools\mole-windows-amd64.exe`, "clean"}, 1, "", "mole clean is not available"},
		{"unsupported command", []string{"mole", "clean"}, 1, "", "mole clean is not available on this platform yet."},
		{"unknown command", []string{"mole", "frobnicate"}, 1, "", "Unknown command: frobnicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.argv, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr %q)", code, tt.wantCode, stderr.String())
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
