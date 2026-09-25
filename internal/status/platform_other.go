//go:build !windows

package status

// commandName is how users invoke Mole on this platform.
const commandName = "mo"

// usageExamples closes the status help text.
const usageExamples = `  mo status                       Interactive dashboard
  mo status --json                One machine-readable snapshot
  mo status --watch --interval 2s Continuous NDJSON stream
`

// systemDiskMount is the mount point of the boot volume.
func systemDiskMount() string { return "/" }

// The hooks below are Windows-only collectors; other platforms keep their
// existing command-based collection paths.

func collectPlatformBatteries() []BatteryStatus { return nil }

func collectPlatformHardware(uint64, []DiskStatus) HardwareInfo { return HardwareInfo{} }

func platformDiskKind(string) (skip bool, external bool) { return false, false }

func platformTrashSize() (size uint64, approx bool, ok bool) { return 0, false, false }

func collectPlatformProxy() ProxyStatus { return ProxyStatus{} }

func platformGPUNames() []GPUStatus { return nil }

func collectPlatformProcesses() (processSample, error) { return processSample{}, nil }

func platformConfigDir() string { return "" }
