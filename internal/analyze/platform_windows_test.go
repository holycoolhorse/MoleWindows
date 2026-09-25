//go:build windows

package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsFilesystemRootWindows(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\`:         true,
		`D:\`:         true,
		`C:\Users`:    false,
		`C:\Users\me`: false,
		"":            false,
	} {
		if got := isFilesystemRoot(path); got != want {
			t.Errorf("isFilesystemRoot(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestUserHomeDirIgnoresPOSIXHome(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", "/c/Users/someone")
	if got := userHomeDir(); !strings.EqualFold(got, profile) {
		t.Fatalf("userHomeDir() = %q, want %q", got, profile)
	}
}

func TestSystemOverviewRootsWindows(t *testing.T) {
	root := t.TempDir()
	programFiles := filepath.Join(root, "Program Files")
	windowsDir := filepath.Join(root, "Windows")
	for _, dir := range []string{programFiles, windowsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "missing"))
	t.Setenv("ProgramData", "")
	t.Setenv("SystemRoot", windowsDir)

	roots := systemOverviewRoots()
	if len(roots) != 2 {
		t.Fatalf("systemOverviewRoots() = %+v, want Program Files and Windows only", roots)
	}
	if roots[0].Path != programFiles || roots[1].Path != windowsDir {
		t.Fatalf("unexpected roots: %+v", roots)
	}
	for _, r := range roots {
		if r.Size != -1 || !r.IsDir {
			t.Fatalf("root %+v should be a pending directory", r)
		}
	}
}

func TestOverviewUsesAppDataAsUserLibrary(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, "AppData"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := createOverviewEntriesWithInsights(nil)
	if len(entries) < 2 || entries[0].Path != home || entries[1].Path != filepath.Join(home, "AppData") {
		t.Fatalf("overview entries = %+v, want Home then AppData", entries)
	}
}

func TestCleanableInsightPathsWindows(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("LOCALAPPDATA", local)
	setTestHome(t, home)
	temp := filepath.Join(local, "Temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range createInsightEntries() {
		if entry.Path == temp && entry.Name == "Temp Files" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Temp Files insight missing for %s", temp)
	}
}

func TestDuSizingDisabledOnWindows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := getDirectorySizeFromDu(t.Context(), dir); err == nil {
		t.Fatal("du sizing must be unavailable on Windows so the Go walk is used")
	}
	size, err := getDirSizeFast(dir)
	if err != nil || size != 4096 {
		t.Fatalf("getDirSizeFast = %d, %v; want 4096 via the Go walk", size, err)
	}
}
