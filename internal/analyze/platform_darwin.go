//go:build darwin

package analyze

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// duSizingAvailable reports whether BSD du(1) is the trusted fast sizer.
const duSizingAvailable = true

// homeLibraryDirName is the per-user library that the overview measures as its
// own row, so Home excludes it to avoid double counting.
const homeLibraryDirName = "Library"

// userLibraryLabel is the overview row name for homeLibraryDirName.
const userLibraryLabel = "User Library"

// trashLabel and fileManagerLabel name the platform delete target and file browser in UI text.
const (
	trashLabel       = "Trash"
	fileManagerLabel = "Finder"
)

// commandName is how users invoke Mole on this platform.
const commandName = "mo"

// usageExamples closes the analyze help text.
const usageExamples = `  mo analyze                 Machine-wide overview
  mo analyze ~/Library       Scan one directory
  mo analyze --json /Volumes Machine-readable output
`

// isFilesystemRoot reports whether path is the root of the scanned tree, where
// skipSystemDirs applies.
func isFilesystemRoot(path string) bool {
	return path == "/"
}

// userHomeDir honors HOME exactly the way the shell side does, so a test that
// repoints HOME sees the analyzer follow it.
func userHomeDir() string {
	return os.Getenv("HOME")
}

var skipSystemDirs = map[string]bool{
	"dev":                     true,
	"tmp":                     true,
	"private":                 true,
	"cores":                   true,
	"net":                     true,
	"home":                    true,
	"System":                  true,
	"sbin":                    true,
	"bin":                     true,
	"etc":                     true,
	"var":                     true,
	"opt":                     false,
	"usr":                     false,
	"Volumes":                 true,
	"Network":                 true,
	".vol":                    true,
	".Spotlight-V100":         true,
	".fseventsd":              true,
	".DocumentRevisions-V100": true,
	".TemporaryItems":         true,
	".MobileBackups":          true,
}

func runSpotlightQuery(ctx context.Context, root, query string) ([]byte, error) {
	return exec.CommandContext(ctx, "mdfind", "-onlyin", root, query).Output()
}

func runLocalSnapshotCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func systemOverviewRoots() []dirEntry {
	return []dirEntry{
		{Name: "Applications", Path: "/Applications", IsDir: true, Size: -1},
		{Name: "System Library", Path: "/Library", IsDir: true, Size: -1},
	}
}

func deviceBackupPaths(home string) []string {
	return []string{filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup")}
}

// cleanableInsightPaths lists paths that silently accumulate space. System
// Caches (~/Library/Caches) is intentionally omitted because the specific
// cache subdirectories below are already its children; listing both would
// double-count the same bytes.
func cleanableInsightPaths(home string) []insightPath {
	paths := []insightPath{
		// Universal (everyone has these)
		{"System Logs", filepath.Join(home, "Library", "Logs")},
		{"Homebrew Cache", filepath.Join(home, "Library", "Caches", "Homebrew")},

		// Developer-specific (only shown if path exists)
		{"Xcode DerivedData", filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData")},
		{"Xcode Simulators", filepath.Join(home, "Library", "Developer", "CoreSimulator", "Devices")},
		{"Xcode Archives", filepath.Join(home, "Library", "Developer", "Xcode", "Archives")},
		{"Spotify Cache", filepath.Join(home, "Library", "Application Support", "Spotify", "PersistentCache")},
		{"JetBrains Cache", filepath.Join(home, "Library", "Caches", "JetBrains")},
		{"Docker Data", filepath.Join(home, "Library", "Containers", "com.docker.docker", "Data")},
		{"pip Cache", filepath.Join(home, "Library", "Caches", "pip")},
		{"uv Cache", filepath.Join(home, ".cache", "uv")},
		{"Gradle Cache", filepath.Join(home, ".gradle", "caches")},
		{"CocoaPods Cache", filepath.Join(home, "Library", "Caches", "CocoaPods")},
	}
	if matches, err := filepath.Glob(filepath.Join(home, "Library", "Group Containers", "*dev.orbstack", "data")); err == nil {
		for _, match := range matches {
			if info, statErr := os.Stat(match); statErr == nil && info.IsDir() {
				paths = append(paths, insightPath{"OrbStack Data", match})
				break
			}
		}
	}
	return paths
}

// moCleanHandledPathFragments marks paths that `mo clean` already reclaims.
var moCleanHandledPathFragments = []string{
	"/Library/Caches/",
	"/Library/Logs/",
	"/Library/Saved Application State/",
	"/.Trash/",
	"/Library/DiagnosticReports/",
}

func diskFreeBytesForPath(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

func openPathCommand(ctx context.Context, path string, reveal bool) *exec.Cmd {
	if reveal {
		return exec.CommandContext(ctx, "open", "-R", path)
	}
	return exec.CommandContext(ctx, "open", path)
}

// Hardlinked files are deduplicated the way `du` does: the first link counts
// its full size and subsequent links seen in the same scan count zero. The
// bool reports whether this call was a deduplicated (zero-counted) hardlink.
// A nil seen map disables deduplication.
func countableFileSize(info fs.FileInfo, seen *sync.Map) (int64, bool) {
	size := getActualFileSize("", info)
	if seen == nil {
		return size, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink <= 1 {
		return size, false
	}
	key := [2]uint64{uint64(uint32(stat.Dev)), stat.Ino}
	if _, loaded := seen.LoadOrStore(key, struct{}{}); loaded {
		return 0, true
	}
	return size, false
}

func getActualFileSize(_ string, info fs.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}

	actualSize := stat.Blocks * 512
	if actualSize < info.Size() {
		return actualSize
	}
	return info.Size()
}

func getLastAccessTimeFromInfo(info fs.FileInfo) time.Time {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(stat.Atimespec.Sec, stat.Atimespec.Nsec)
}
