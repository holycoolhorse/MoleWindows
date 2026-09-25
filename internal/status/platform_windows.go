//go:build windows

package status

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// commandName is how users invoke Mole on this platform.
const commandName = "mole"

// usageExamples closes the status help text.
const usageExamples = `  mole status                       Interactive dashboard
  mole status --json                One machine-readable snapshot
  mole status --watch --interval 2s Continuous NDJSON stream
`

var (
	modKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemPowerStatus = modKernel32.NewProc("GetSystemPowerStatus")
	modShell32               = windows.NewLazySystemDLL("shell32.dll")
	procSHQueryRecycleBinW   = modShell32.NewProc("SHQueryRecycleBinW")
	modUser32                = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplaySettingsW = modUser32.NewProc("EnumDisplaySettingsW")
)

// systemDiskMount is the mount point gopsutil reports for the boot volume.
func systemDiskMount() string {
	if drive := os.Getenv("SystemDrive"); drive != "" {
		return strings.ToUpper(drive)
	}
	return "C:"
}

// ---- Battery ----

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var (
	batteryHealthMu       sync.Mutex
	batteryHealthAt       time.Time
	batteryHealthCycles   int
	batteryHealthCapacity int
)

func collectPlatformBatteries() []BatteryStatus {
	var ps systemPowerStatus
	if ret, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps))); ret == 0 {
		return nil
	}
	batt, ok := batteryFromWindowsPower(windowsPowerStatus{
		ACLineStatus:       ps.ACLineStatus,
		BatteryFlag:        ps.BatteryFlag,
		BatteryLifePercent: ps.BatteryLifePercent,
		BatteryLifeTime:    ps.BatteryLifeTime,
	})
	if !ok {
		return nil
	}
	batt.CycleCount, batt.Capacity = cachedWindowsBatteryHealth()
	return []BatteryStatus{batt}
}

// cachedWindowsBatteryHealth reads cycle count and capacity from the ACPI
// battery WMI classes. WMI is slow and the values change over weeks, so the
// reading is cached like the macOS system_profiler data.
func cachedWindowsBatteryHealth() (cycles int, capacity int) {
	batteryHealthMu.Lock()
	defer batteryHealthMu.Unlock()
	if !batteryHealthAt.IsZero() && time.Since(batteryHealthAt) < powerCacheTTL {
		return batteryHealthCycles, batteryHealthCapacity
	}
	batteryHealthCycles, batteryHealthCapacity = readWindowsBatteryHealth()
	batteryHealthAt = time.Now()
	return batteryHealthCycles, batteryHealthCapacity
}

type wmiFullChargedCapacity struct {
	FullChargedCapacity uint32
}

type wmiBatteryStaticData struct {
	DesignedCapacity uint32
}

type wmiBatteryCycleCount struct {
	CycleCount uint32
}

// readWindowsBatteryHealth runs every WMI query on one goroutine and waits a
// bounded time for it: a wedged WMI service must never stall status. On a
// timeout the goroutine owns its buffers, so nothing is read while it writes.
func readWindowsBatteryHealth() (cycles int, capacity int) {
	type result struct{ cycles, capacity int }
	done := make(chan result, 1)
	go func() {
		var full []wmiFullChargedCapacity
		var static []wmiBatteryStaticData
		var cycleRows []wmiBatteryCycleCount
		_ = wmi.QueryNamespace("SELECT FullChargedCapacity FROM BatteryFullChargedCapacity", &full, `root\WMI`)
		_ = wmi.QueryNamespace("SELECT DesignedCapacity FROM BatteryStaticData", &static, `root\WMI`)
		_ = wmi.QueryNamespace("SELECT CycleCount FROM BatteryCycleCount", &cycleRows, `root\WMI`)
		var r result
		if len(full) > 0 && len(static) > 0 {
			r.capacity = batteryCapacityPercent(full[0].FullChargedCapacity, static[0].DesignedCapacity)
		}
		if len(cycleRows) > 0 {
			r.cycles = int(cycleRows[0].CycleCount)
		}
		done <- r
	}()
	select {
	case r := <-done:
		return r.cycles, r.capacity
	case <-time.After(3 * time.Second):
		return 0, 0
	}
}

// ---- Hardware ----

func collectPlatformHardware(totalRAM uint64, disks []DiskStatus) HardwareInfo {
	manufacturer := readRegistryString(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, "SystemManufacturer")
	product := readRegistryString(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName")
	cpuModel := strings.Join(strings.Fields(readRegistryString(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString")), " ")

	const currentVersion = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	osVersion := windowsOSLabel(
		readRegistryString(registry.LOCAL_MACHINE, currentVersion, "ProductName"),
		readRegistryString(registry.LOCAL_MACHINE, currentVersion, "DisplayVersion"),
		readRegistryString(registry.LOCAL_MACHINE, currentVersion, "CurrentBuildNumber"),
	)

	diskSize := "Unknown"
	if disk, ok := rootDisk(disks); ok {
		diskSize = humanBytes(disk.Total)
	}

	if cpuModel == "" {
		cpuModel = "Unknown"
	}
	return HardwareInfo{
		Model:       windowsModelLabel(manufacturer, product),
		CPUModel:    cpuModel,
		TotalRAM:    humanBytes(totalRAM),
		DiskSize:    diskSize,
		OSVersion:   osVersion,
		RefreshRate: primaryDisplayRefreshRate(),
	}
}

func readRegistryString(root registry.Key, path, name string) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close() //nolint:errcheck // read-only registry handle
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func readRegistryInteger(root registry.Key, path, name string) uint64 {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer key.Close() //nolint:errcheck // read-only registry handle
	value, _, err := key.GetIntegerValue(name)
	if err != nil {
		return 0
	}
	return value
}

// DEVMODEW field offsets (wingdi.h); the struct is 220 bytes.
const (
	devModeSize               = 220
	devModeSizeOffset         = 68
	devModeDisplayFreqOffset  = 184
	enumCurrentSettings       = 0xFFFFFFFF
	minPlausibleRefreshRateHz = 2
)

func primaryDisplayRefreshRate() string {
	var devMode [devModeSize]byte
	*(*uint16)(unsafe.Pointer(&devMode[devModeSizeOffset])) = devModeSize
	ret, _, _ := procEnumDisplaySettingsW.Call(0, uintptr(enumCurrentSettings), uintptr(unsafe.Pointer(&devMode[0])))
	if ret == 0 {
		return ""
	}
	hz := *(*uint32)(unsafe.Pointer(&devMode[devModeDisplayFreqOffset]))
	// 0 and 1 mean "hardware default"; they are not a real rate.
	if hz < minPlausibleRefreshRateHz {
		return ""
	}
	return fmt.Sprintf("%dHz", hz)
}

// ---- Disks ----

// platformDiskKind classifies a Windows drive: removable drives are external;
// network, optical, and RAM drives are skipped; everything else, including a
// drive whose type cannot be read, is shown as internal.
func platformDiskKind(mount string) (skip bool, external bool) {
	root := mount
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return true, false
	}
	switch windows.GetDriveType(p) {
	case windows.DRIVE_REMOVABLE:
		return false, true
	case windows.DRIVE_REMOTE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK:
		return true, false
	default:
		return false, false
	}
}

type shQueryRBInfo struct {
	cbSize      uint32
	i64Size     int64
	i64NumItems int64
}

// platformTrashSize sums the Recycle Bin across all drives in one shell call.
func platformTrashSize() (uint64, bool, bool) {
	info := shQueryRBInfo{cbSize: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	ret, _, _ := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info)))
	if ret != 0 || info.i64Size < 0 {
		return 0, false, true
	}
	return uint64(info.i64Size), false, true
}

// ---- Proxy ----

func collectPlatformProxy() ProxyStatus {
	const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	return proxyFromWindowsSettings(
		readRegistryInteger(registry.CURRENT_USER, internetSettings, "ProxyEnable"),
		readRegistryString(registry.CURRENT_USER, internetSettings, "ProxyServer"),
		readRegistryString(registry.CURRENT_USER, internetSettings, "AutoConfigURL"),
	)
}

// ---- GPU ----

// displayAdapterClass is the device-setup class GUID for display adapters.
const displayAdapterClass = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

// platformGPUNames lists display adapters from the device registry. Live
// utilization needs vendor tooling, so it is only reported through nvidia-smi.
func platformGPUNames() []GPUStatus {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, displayAdapterClass, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer key.Close() //nolint:errcheck // read-only registry handle
	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var gpus []GPUStatus
	for _, sub := range subkeys {
		if len(sub) != 4 { // 0000, 0001, ...; skips "Properties" and "Configuration".
			continue
		}
		name := readRegistryString(registry.LOCAL_MACHINE, displayAdapterClass+`\`+sub, "DriverDesc")
		if name == "" || seen[name] || strings.Contains(strings.ToLower(name), "basic display") ||
			strings.Contains(strings.ToLower(name), "remote display") {
			continue
		}
		seen[name] = true
		gpus = append(gpus, GPUStatus{Name: name, Note: "Usage requires nvidia-smi"})
	}
	return gpus
}

// ---- Processes ----

var (
	processTrackerMu sync.Mutex
	processTracker   processCPUTracker
)

func collectPlatformProcesses() (processSample, error) {
	buf, err := querySystemProcessInformation()
	if err != nil {
		return processSample{}, err
	}

	var totalRAM uint64
	if vm, err := mem.VirtualMemory(); err == nil {
		totalRAM = vm.Total
	}

	type row struct {
		key  processKey
		ppid int
		name string
		ws   uint64
	}
	var rows []row
	cpuTimes := make(map[processKey]int64)
	for offset := uint32(0); int(offset) < len(buf); {
		info := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset]))
		pid := int(info.UniqueProcessID)
		// PID 0 is the System Idle Process: its "CPU" is idle time.
		if pid != 0 {
			key := processKey{pid: pid, createTime: info.CreateTime}
			cpuTimes[key] = info.UserTime + info.KernelTime
			name := ""
			if info.ImageName.Buffer != nil && info.ImageName.Length > 0 {
				name = windows.UTF16PtrToString(info.ImageName.Buffer)
				if n := int(info.ImageName.Length / 2); n < len(name) {
					name = name[:n]
				}
			}
			if name == "" && pid == 4 {
				name = "System"
			}
			rows = append(rows, row{key: key, ppid: int(info.InheritedFromUniqueProcessID), name: name, ws: uint64(info.WorkingSetSize)})
		}
		if info.NextEntryOffset == 0 {
			break
		}
		offset += info.NextEntryOffset
	}

	// FILETIME-scaled wall clock (100ns units since 1601), matching CreateTime.
	nowWall := time.Now().UnixNano()/100 + 116444736000000000
	processTrackerMu.Lock()
	percents := processTracker.update(nowWall, cpuTimes)
	processTrackerMu.Unlock()

	procs := make([]ProcessInfo, 0, len(rows))
	for _, r := range rows {
		memPercent := 0.0
		if totalRAM > 0 {
			memPercent = float64(r.ws) * 100 / float64(totalRAM)
		}
		display := strings.TrimSuffix(r.name, ".exe")
		procs = append(procs, ProcessInfo{
			PID:         r.key.pid,
			PPID:        r.ppid,
			Name:        display,
			Command:     r.name,
			CPU:         percents[r.key],
			Memory:      memPercent,
			MemoryBytes: r.ws,
		})
	}
	// Zombie state does not exist on Windows; parents are still reported.
	return processSample{processes: procs, parentsAvailable: true}, nil
}

func querySystemProcessInformation() ([]byte, error) {
	size := uint32(512 * 1024)
	for range 8 {
		buf := make([]byte, size)
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &needed)
		if err == nil {
			return buf, nil
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, err
		}
		// The process list can grow between calls; leave headroom.
		size = max(needed, size) + 64*1024
	}
	return nil, errors.New("process list kept growing")
}

// ---- Misc ----

// platformConfigDir is where status preferences live: %APPDATA%\mole.
func platformConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "mole")
	}
	return ""
}
