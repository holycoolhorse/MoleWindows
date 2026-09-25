//go:build windows

package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectPlatformProcessesIncludesSelf(t *testing.T) {
	sample, err := collectPlatformProcesses()
	if err != nil {
		t.Fatalf("collectPlatformProcesses() error = %v", err)
	}
	if !sample.parentsAvailable {
		t.Fatal("parents should be available from SYSTEM_PROCESS_INFORMATION")
	}
	self := os.Getpid()
	for _, p := range sample.processes {
		if p.PID == 0 {
			t.Fatal("System Idle Process must be excluded")
		}
		if p.PID == self {
			if p.Name == "" || strings.HasSuffix(strings.ToLower(p.Name), ".exe") {
				t.Fatalf("self process name = %q, want a name without .exe", p.Name)
			}
			if p.MemoryBytes == 0 {
				t.Fatal("self process working set should be non-zero")
			}
			return
		}
	}
	t.Fatalf("own pid %d missing from %d processes", self, len(sample.processes))
}

// A non-ASCII image name must survive intact: NTUnicodeString.Length counts
// UTF-16 bytes, not bytes of the decoded UTF-8 string.
func TestCollectPlatformProcessesKeepsUnicodeNames(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "ünïçødé日本"
	copyPath := filepath.Join(t.TempDir(), name+".exe")
	if err := os.WriteFile(copyPath, src, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(copyPath, "-test.run=^TestProcessSleeperHelper$")
	cmd.Env = append(os.Environ(), "MOLE_PROCESS_SLEEPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sample, err := collectPlatformProcesses()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range sample.processes {
			if p.PID == cmd.Process.Pid {
				if p.Name != name || p.Command != name+".exe" {
					t.Fatalf("process name = %q, command = %q; want %q and %q", p.Name, p.Command, name, name+".exe")
				}
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("helper process %d never appeared", cmd.Process.Pid)
}

// TestProcessSleeperHelper only idles so its parent can inspect it.
func TestProcessSleeperHelper(t *testing.T) {
	if os.Getenv("MOLE_PROCESS_SLEEPER") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(30 * time.Second)
}

func TestCollectPlatformHardwareReportsWindows(t *testing.T) {
	hw := collectPlatformHardware(16<<30, nil)
	if !strings.HasPrefix(hw.OSVersion, "Windows") {
		t.Fatalf("OSVersion = %q, want a Windows label", hw.OSVersion)
	}
	if hw.TotalRAM == "" || hw.Model == "" || hw.CPUModel == "" {
		t.Fatalf("hardware fields should never be empty: %+v", hw)
	}
}

func TestPlatformTrashSizeQueriesRecycleBin(t *testing.T) {
	if _, approx, ok := platformTrashSize(); ok && approx {
		t.Fatal("SHQueryRecycleBinW is exact; approx must be false")
	}
}

func TestSystemDiskMountUsesSystemDrive(t *testing.T) {
	t.Setenv("SystemDrive", "d:")
	if got := systemDiskMount(); got != "D:" {
		t.Fatalf("systemDiskMount() = %q, want D:", got)
	}
}

func TestPlatformDiskKindSystemDriveIsInternal(t *testing.T) {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	skip, external := platformDiskKind(drive)
	if skip || external {
		t.Fatalf("platformDiskKind(%q) = skip %v, external %v; want an internal disk", drive, skip, external)
	}
}
