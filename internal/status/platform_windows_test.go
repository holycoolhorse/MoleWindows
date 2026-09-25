//go:build windows

package status

import (
	"os"
	"strings"
	"testing"
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
