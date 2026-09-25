//go:build windows

package analyze

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// fakeWindowsLayout points every environment variable the protection policy
// reads at a temp tree, so the table below is deterministic on any machine.
func fakeWindowsLayout(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	users := filepath.Join(root, "Users")
	home := filepath.Join(users, "alice")
	local := filepath.Join(home, "AppData", "Local")
	roaming := filepath.Join(home, "AppData", "Roaming")
	for _, dir := range []string{
		filepath.Join(root, "Windows", "System32"),
		filepath.Join(root, "Program Files", "App"),
		filepath.Join(root, "Program Files (x86)", "App"),
		filepath.Join(root, "ProgramData", "Microsoft", "Windows"),
		filepath.Join(root, "ProgramData", "Package Cache", "{guid}"),
		filepath.Join(root, "ProgramData", "SomeVendor", "cache"),
		filepath.Join(users, "bob", "Documents"),
		filepath.Join(local, "Temp", "junk"),
		filepath.Join(local, "Packages", "App_1"),
		filepath.Join(local, "Google", "Chrome", "User Data", "Default", "Cache"),
		filepath.Join(roaming, "Microsoft", "Credentials"),
		filepath.Join(home, "Downloads", "old"),
		filepath.Join(home, "OneDrive", "notes"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	t.Setenv("SystemRoot", filepath.Join(root, "Windows"))
	t.Setenv("windir", filepath.Join(root, "Windows"))
	t.Setenv("ProgramFiles", filepath.Join(root, "Program Files"))
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "Program Files (x86)"))
	t.Setenv("ProgramW6432", filepath.Join(root, "Program Files"))
	t.Setenv("ProgramData", filepath.Join(root, "ProgramData"))
	t.Setenv("PUBLIC", filepath.Join(users, "Public"))
	t.Setenv("SystemDrive", filepath.VolumeName(root))
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("TEMP", filepath.Join(local, "Temp"))
	t.Setenv("TMP", filepath.Join(local, "Temp"))
	t.Setenv("OneDrive", filepath.Join(home, "OneDrive"))
	return root
}

func TestIsProtectedAnalyzeDeletePathWindows(t *testing.T) {
	root := fakeWindowsLayout(t)
	users := filepath.Join(root, "Users")
	home := filepath.Join(users, "alice")
	local := filepath.Join(home, "AppData", "Local")
	volume := filepath.VolumeName(root) + `\`

	tests := []struct {
		name string
		path string
		want bool
	}{
		// Volume roots and OS-owned root entries.
		{"volume root", volume, true},
		{"page file", filepath.Join(volume, "pagefile.sys"), true},
		{"recycle bin", filepath.Join(volume, "$Recycle.Bin", "S-1-5-21"), true},
		{"system volume information", filepath.Join(volume, "System Volume Information"), true},
		{"upgrade staging", filepath.Join(volume, "$Windows.~BT", "Sources"), true},

		// OS, programs, and MSI sources: whole trees.
		{"windows dir", filepath.Join(root, "Windows"), true},
		{"system32 child", filepath.Join(root, "Windows", "System32"), true},
		{"case-folded system32", strings.ToUpper(filepath.Join(root, "windows", "system32")), true},
		{"trailing separator", filepath.Join(root, "Windows") + `\`, true},
		{"program files app", filepath.Join(root, "Program Files", "App"), true},
		{"program files x86 app", filepath.Join(root, "Program Files (x86)", "App"), true},
		{"programdata microsoft", filepath.Join(root, "ProgramData", "Microsoft", "Windows"), true},
		{"package cache", filepath.Join(root, "ProgramData", "Package Cache", "{guid}"), true},
		{"programdata root", filepath.Join(root, "ProgramData"), true},

		// Profiles: every account root, and this user's special folders.
		{"users dir", users, true},
		{"other account", filepath.Join(users, "bob"), true},
		{"own profile", home, true},
		{"appdata", filepath.Join(home, "AppData"), true},
		{"appdata local", local, true},
		{"temp root", filepath.Join(local, "Temp"), true},
		{"uwp packages", filepath.Join(local, "Packages", "App_1"), true},
		{"credentials", filepath.Join(home, "AppData", "Roaming", "Microsoft", "Credentials"), true},
		{"onedrive root", filepath.Join(home, "OneDrive"), true},

		// Path shapes that dodge drive-letter checks.
		{"unc share", `\\server\share\folder`, true},
		{"device path", `\\?\C:\Temp\x`, true},
		{"alternate data stream", filepath.Join(local, "Temp", "junk") + ":stream", true},

		// Ordinary user data stays deletable.
		{"temp child", filepath.Join(local, "Temp", "junk"), false},
		{"browser cache", filepath.Join(local, "Google", "Chrome", "User Data", "Default", "Cache"), false},
		{"downloads child", filepath.Join(home, "Downloads", "old"), false},
		{"other account data", filepath.Join(users, "bob", "Documents"), false},
		{"onedrive child", filepath.Join(home, "OneDrive", "notes"), false},
		{"vendor programdata", filepath.Join(root, "ProgramData", "SomeVendor", "cache"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProtectedAnalyzeDeletePath(tt.path); got != tt.want {
				t.Fatalf("isProtectedAnalyzeDeletePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// stubRecycleBin captures the paths that reach the shell sink.
func stubRecycleBin(t *testing.T, driveType uint32, moverErr error) *[]string {
	t.Helper()
	var calls []string
	origMover, origDrive := recycleBinMover, driveTypeForPath
	recycleBinMover = func(absPath string) error {
		calls = append(calls, absPath)
		if moverErr != nil {
			return moverErr
		}
		return os.RemoveAll(absPath)
	}
	driveTypeForPath = func(string) uint32 { return driveType }
	t.Cleanup(func() {
		recycleBinMover, driveTypeForPath = origMover, origDrive
	})
	return &calls
}

func TestMoveToTrashWindowsRoutesThroughRecycleBin(t *testing.T) {
	root := fakeWindowsLayout(t)
	target := filepath.Join(root, "Users", "alice", "AppData", "Local", "Temp", "junk")
	calls := stubRecycleBin(t, windows.DRIVE_FIXED, nil)

	if err := moveToTrash(target); err != nil {
		t.Fatalf("moveToTrash(%q) error = %v", target, err)
	}
	if len(*calls) != 1 || !strings.EqualFold((*calls)[0], target) {
		t.Fatalf("recycle bin calls = %v, want [%s]", *calls, target)
	}
}

func TestMoveToTrashWindowsRefusesProtectedPathBeforeSink(t *testing.T) {
	root := fakeWindowsLayout(t)
	calls := stubRecycleBin(t, windows.DRIVE_FIXED, nil)

	for _, path := range []string{
		filepath.Join(root, "Windows", "System32"),
		filepath.Join(root, "Users", "bob"),
		filepath.Join(root, "Users", "alice", "AppData", "Local", "Temp", "..", "..", "Local"),
	} {
		if err := moveToTrash(path); err == nil {
			t.Fatalf("moveToTrash(%q) succeeded, want refusal", path)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("protected paths reached the sink: %v", *calls)
	}
}

func TestMoveToTrashWindowsRefusesDrivesWithoutRecycleBin(t *testing.T) {
	root := fakeWindowsLayout(t)
	target := filepath.Join(root, "Users", "alice", "Downloads", "old")

	for _, driveType := range []uint32{windows.DRIVE_REMOVABLE, windows.DRIVE_REMOTE, windows.DRIVE_RAMDISK, windows.DRIVE_UNKNOWN} {
		calls := stubRecycleBin(t, driveType, nil)
		err := moveToTrash(target)
		if !errors.Is(err, errRecycleBinUnavailable) {
			t.Fatalf("drive type %d: err = %v, want errRecycleBinUnavailable", driveType, err)
		}
		if len(*calls) != 0 {
			t.Fatalf("drive type %d reached the sink: %v", driveType, *calls)
		}
		if _, statErr := os.Stat(target); statErr != nil {
			t.Fatalf("drive type %d: target was removed: %v", driveType, statErr)
		}
	}
}

func TestMoveToTrashWindowsReportsSinkFailureAndLeftovers(t *testing.T) {
	root := fakeWindowsLayout(t)
	target := filepath.Join(root, "Users", "alice", "Downloads", "old")

	stubRecycleBin(t, windows.DRIVE_FIXED, errors.New("shell error 0x78"))
	if err := moveToTrash(target); err == nil || !strings.Contains(err.Error(), "0x78") {
		t.Fatalf("moveToTrash error = %v, want the shell error", err)
	}

	// A sink that reports success but leaves the path behind is a failure.
	origMover := recycleBinMover
	recycleBinMover = func(string) error { return nil }
	t.Cleanup(func() { recycleBinMover = origMover })
	if err := moveToTrash(target); err == nil || !strings.Contains(err.Error(), "still present") {
		t.Fatalf("moveToTrash error = %v, want still-present failure", err)
	}
}

func TestMoveToRecycleBinRealShellCall(t *testing.T) {
	if os.Getenv("MOLE_TEST_REAL_RECYCLE_BIN") != "1" {
		t.Skip("set MOLE_TEST_REAL_RECYCLE_BIN=1 to exercise SHFileOperationW")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "mole-recycle-probe.txt")
	if err := os.WriteFile(target, []byte("probe"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	if err := moveToRecycleBin(target); err != nil {
		t.Fatalf("moveToRecycleBin: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("probe still present after recycle: %v", err)
	}
}

func TestValidateWindowsPathShape(t *testing.T) {
	for path, wantErr := range map[string]bool{
		`C:\Users\alice\file.txt`:        false,
		`D:\data`:                        false,
		`\\server\share\x`:               true,
		`\\?\C:\x`:                       true,
		`//server/share`:                 true,
		`C:\Users\alice\file.txt:hidden`: true,
		`\Users\alice`:                   true,
	} {
		if err := validateWindowsPathShape(path); (err != nil) != wantErr {
			t.Errorf("validateWindowsPathShape(%q) error = %v, wantErr %v", path, err, wantErr)
		}
	}
}

func TestPathWithinFold(t *testing.T) {
	tests := []struct {
		path, root string
		want       bool
	}{
		{`C:\Windows`, `C:\Windows`, true},
		{`c:\windows\system32`, `C:\Windows`, true},
		{`C:\WindowsApps`, `C:\Windows`, false},
		{`C:\`, `C:\`, true},
		{`C:\anything`, `C:\`, true},
	}
	for _, tt := range tests {
		if got := pathWithinFold(tt.path, tt.root); got != tt.want {
			t.Errorf("pathWithinFold(%q, %q) = %v, want %v", tt.path, tt.root, got, tt.want)
		}
	}
}

func TestRecycleBinDriveTypeResolvesRealVolume(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := recycleBinDriveType(file), volumeDriveType(filepath.VolumeName(file)+`\`); got != want {
		t.Fatalf("recycleBinDriveType(%q) = %d, want the volume's own type %d", file, got, want)
	}
	if got := recycleBinDriveType(filepath.Join(dir, "missing")); got != windows.DRIVE_UNKNOWN {
		t.Fatalf("unresolvable path drive type = %d, want DRIVE_UNKNOWN", got)
	}
}
