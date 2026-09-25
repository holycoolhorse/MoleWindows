//go:build windows

package analyze

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// duSizingAvailable is false on Windows: there is no system du, and a du.exe
// found on PATH (Git for Windows, MSYS2, Cygwin) has different flags and path
// semantics. Every du caller already falls back to the Go walk.
const duSizingAvailable = false

// homeLibraryDirName is the per-user application data tree. The overview
// measures it as its own row, so Home excludes it to avoid double counting.
const homeLibraryDirName = "AppData"

// userLibraryLabel is the overview row name for homeLibraryDirName.
const userLibraryLabel = "AppData"

// trashLabel and fileManagerLabel name the platform delete target and file browser in UI text.
const (
	trashLabel       = "Recycle Bin"
	fileManagerLabel = "File Explorer"
)

// commandName is how users invoke Mole on this platform.
const commandName = "mole"

// usageExamples closes the analyze help text.
const usageExamples = `  mole analyze                   Machine-wide overview
  mole analyze %LOCALAPPDATA%    Scan one directory
  mole analyze --json D:\        Machine-readable output
`

// isFilesystemRoot reports whether path is a volume root such as C:\, where
// skipSystemDirs applies.
func isFilesystemRoot(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	return filepath.Dir(clean) == clean
}

// userHomeDir resolves the profile directory (%USERPROFILE%). HOME is ignored
// because shells such as Git Bash export it in a POSIX form.
func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}

// skipSystemDirs are volume-root entries that are either OS-owned staging and
// recovery areas or junction aliases that would double count other trees.
var skipSystemDirs = map[string]bool{
	"$Recycle.Bin":              true,
	"$RECYCLE.BIN":              true,
	"System Volume Information": true,
	"$WinREAgent":               true,
	"$SysReset":                 true,
	"$Windows.~BT":              true,
	"$Windows.~WS":              true,
	"Config.Msi":                true,
	"Recovery":                  true,
	"Documents and Settings":    true,
}

var errSpotlightUnavailable = errors.New("spotlight is not available on Windows")

// runSpotlightQuery has no Windows equivalent; large files come from the walk.
func runSpotlightQuery(context.Context, string, string) ([]byte, error) {
	return nil, errSpotlightUnavailable
}

// runLocalSnapshotCommand reports no Time Machine snapshots on Windows.
func runLocalSnapshotCommand(context.Context, string, ...string) ([]byte, error) {
	return nil, nil
}

func systemOverviewRoots() []dirEntry {
	var roots []dirEntry
	seen := make(map[string]bool)
	add := func(name, path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		seen[key] = true
		roots = append(roots, dirEntry{Name: name, Path: path, IsDir: true, Size: -1})
	}
	add("Program Files", os.Getenv("ProgramFiles"))
	add("Program Files (x86)", os.Getenv("ProgramFiles(x86)"))
	add("ProgramData", os.Getenv("ProgramData"))
	add("Windows", os.Getenv("SystemRoot"))
	return roots
}

func deviceBackupPaths(home string) []string {
	paths := []string{
		// Microsoft Store build of Apple Devices / iTunes.
		filepath.Join(home, "Apple", "MobileSync", "Backup"),
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		// Classic iTunes installer.
		paths = append(paths, filepath.Join(appData, "Apple Computer", "MobileSync", "Backup"))
	}
	return paths
}

// cleanableInsightPaths lists rebuildable caches that silently accumulate
// space. Only rows whose directory exists are shown.
func cleanableInsightPaths(home string) []insightPath {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	return []insightPath{
		{"Temp Files", filepath.Join(localAppData, "Temp")},
		{"Crash Dumps", filepath.Join(localAppData, "CrashDumps")},
		{"Internet Cache", filepath.Join(localAppData, "Microsoft", "Windows", "INetCache")},
		{"npm Cache", filepath.Join(localAppData, "npm-cache")},
		{"pip Cache", filepath.Join(localAppData, "pip", "Cache")},
		{"uv Cache", filepath.Join(localAppData, "uv", "cache")},
		{"Yarn Cache", filepath.Join(localAppData, "Yarn", "Cache")},
		{"NuGet Cache", filepath.Join(localAppData, "NuGet", "v3-cache")},
		{"Go Build Cache", filepath.Join(localAppData, "go-build")},
		{"JetBrains Cache", filepath.Join(localAppData, "JetBrains")},
		{"Gradle Cache", filepath.Join(home, ".gradle", "caches")},
	}
}

// moCleanHandledPathFragments is empty: `mo clean` is not available on
// Windows yet, so no path is labeled as handled by it.
var moCleanHandledPathFragments []string

func diskFreeBytesForPath(path string) int64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &free); err != nil {
		return 0
	}
	return int64(freeToCaller)
}

// openPathCommand opens path with its default handler, or reveals it in
// Explorer. explorer.exe is resolved from %SystemRoot% so a same-named binary
// earlier on PATH cannot receive the request.
func openPathCommand(ctx context.Context, path string, reveal bool) *exec.Cmd {
	explorer := "explorer.exe"
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		explorer = filepath.Join(systemRoot, "explorer.exe")
	}
	cmd := exec.CommandContext(ctx, explorer)
	// explorer.exe parses its own command line; pass it verbatim so the path
	// is quoted exactly once.
	if reveal {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + explorer + `" /select,"` + path + `"`}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + explorer + `" "` + path + `"`}
	}
	return cmd
}

// countableFileSize returns the size to attribute to a file. NTFS hardlinks
// are not deduplicated: the link count is not available from a directory
// listing without opening every file.
func countableFileSize(path string, info fs.FileInfo, _ *sync.Map) (int64, bool) {
	return getActualFileSize(path, info), false
}

// File attributes that change how many bytes a file occupies on disk.
const (
	fileAttributeSparse             = 0x00000200
	fileAttributeCompressed         = 0x00000800
	fileAttributeOffline            = 0x00001000
	fileAttributeRecallOnOpen       = 0x00040000
	fileAttributeRecallOnDataAccess = 0x00400000
)

var procGetCompressedFileSizeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")

// getActualFileSize returns the bytes a file occupies on this disk, which is
// what deleting it would free. Cloud placeholders (OneDrive "online-only",
// which Known Folder Move turns on for Desktop and Documents) hold no local
// data, so they count as zero. Compressed and sparse files report their
// allocated size when the path is known; otherwise the logical size.
func getActualFileSize(path string, info fs.FileInfo) int64 {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return info.Size()
	}
	return windowsOnDiskSize(path, data.FileAttributes, info.Size())
}

func windowsOnDiskSize(path string, attrs uint32, logical int64) int64 {
	if attrs&(fileAttributeOffline|fileAttributeRecallOnOpen|fileAttributeRecallOnDataAccess) != 0 {
		return 0
	}
	if attrs&(fileAttributeSparse|fileAttributeCompressed) == 0 || path == "" {
		return logical
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return logical
	}
	var high uint32
	low, _, callErr := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xFFFFFFFF && callErr != windows.ERROR_SUCCESS {
		return logical
	}
	if onDisk := int64(high)<<32 | int64(uint32(low)); onDisk >= 0 && onDisk < logical {
		return onDisk
	}
	return logical
}

func getLastAccessTimeFromInfo(info fs.FileInfo) time.Time {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, data.LastAccessTime.Nanoseconds())
}
