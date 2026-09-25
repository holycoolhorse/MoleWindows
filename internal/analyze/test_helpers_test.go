//go:build darwin || windows

package analyze

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipIfFinderUnavailable(t *testing.T) {
	t.Helper()

	if os.Getenv("CI") != "" {
		t.Skip("Skipping Finder-dependent test in CI")
	}
	if os.Getenv("MOLE_SKIP_FINDER_TESTS") == "1" {
		t.Skip("Skipping Finder-dependent test via MOLE_SKIP_FINDER_TESTS")
	}
	if _, err := exec.LookPath("osascript"); err != nil {
		t.Skipf("Skipping Finder-dependent test, osascript unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", `tell application "Finder" to get name`)
	output, err := cmd.CombinedOutput()
	text := strings.ToLower(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		t.Skip("Skipping Finder-dependent test, Finder probe timed out")
	}
	if strings.Contains(text, "connection invalid") || strings.Contains(text, "can’t get application \"finder\"") || strings.Contains(text, "can't get application \"finder\"") {
		t.Skipf("Skipping Finder-dependent test, Finder probe indicates unavailable session: %s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		reason := strings.TrimSpace(string(output))
		if reason == "" {
			reason = err.Error()
		}
		t.Skipf("Skipping Finder-dependent test, Finder unavailable: %s", reason)
	}
}

// setTestHome points every variable userHomeDir and os.UserHomeDir may read at
// home: HOME on macOS, %USERPROFILE% on Windows.
func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// skipUnlessDarwin skips a case that exercises a macOS-only mechanism.
func skipUnlessDarwin(t *testing.T, reason string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only: " + reason)
	}
}

// requireSymlinks skips when this machine cannot create a working symlink,
// as on Windows without Developer Mode or elevation.
func requireSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write symlink probe target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}
