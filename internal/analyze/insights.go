//go:build darwin || windows

package analyze

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// createInsightEntries returns the list of hidden-space insight entries
// to show in the overview screen alongside the standard directory entries.
func createInsightEntries() []dirEntry {
	home := userHomeDir()
	if home == "" {
		return nil
	}

	var entries []dirEntry

	// Device backups (iOS/iPadOS) stored by Finder or iTunes.
	for _, backupPath := range deviceBackupPaths(home) {
		if info, err := os.Stat(backupPath); err == nil && info.IsDir() {
			entries = append(entries, dirEntry{
				Name:  "iOS Backups",
				Path:  backupPath,
				IsDir: true,
				Size:  -1,
			})
			break
		}
	}

	// Old Downloads: ~/Downloads (files older than 90 days)
	downloadsPath := filepath.Join(home, "Downloads")
	if info, err := os.Stat(downloadsPath); err == nil && info.IsDir() {
		entries = append(entries, dirEntry{
			Name:  "Old Downloads (90d+)",
			Path:  downloadsPath,
			IsDir: true,
			Size:  -1,
		})
	}

	// Cleanable paths: things the user can safely delete or rebuild. Each
	// platform lists its own; only paths that exist are shown.
	for _, c := range cleanableInsightPaths(home) {
		if info, err := os.Stat(c.path); err == nil && info.IsDir() {
			entries = append(entries, dirEntry{
				Name:  c.name,
				Path:  c.path,
				IsDir: true,
				Size:  -1,
			})
		}
	}

	return entries
}

// insightPath is one named overview insight row.
type insightPath struct {
	name string
	path string
}

// measureInsightSize measures the size of a path.
// Old Downloads is treated specially: only files older than 90 days are counted.
func measureInsightSize(path string) (int64, error) {
	home := userHomeDir()

	if home != "" && path == filepath.Join(home, "Downloads") {
		return measureOldDownloads(path, 90)
	}

	return measureOverviewSize(path)
}

// measureOldDownloads calculates total size of files in a directory
// that haven't been modified in the given number of days.
func measureOldDownloads(dir string, daysOld int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -daysOld)
	var total int64

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		// Skip hidden files.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			if entry.IsDir() {
				// Use du for directories.
				if size, err := getDirSizeFast(filepath.Join(dir, entry.Name())); err == nil {
					total += size
				}
			} else {
				total += info.Size()
			}
		}
	}

	return total, nil
}

// getDirSizeFast measures directory size using du.
func getDirSizeFast(path string) (int64, error) {
	if !duSizingAvailable {
		return getDirectoryLogicalSizeWithExclude(path, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "du", "-sk", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return 0, nil
	}

	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}

	return kb * 1024, nil
}
