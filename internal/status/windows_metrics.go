package status

// Platform-neutral helpers for the Windows collectors. They hold no syscalls,
// so their parsing and arithmetic are unit tested on every platform.

import (
	"fmt"
	"strings"
)

// Windows SYSTEM_POWER_STATUS sentinel values.
const (
	winACOnline           = 1
	winBatteryCharging    = 0x08
	winBatteryNoSystem    = 0x80
	winBatteryFlagUnknown = 0xFF
	winUnknownPercent     = 0xFF
	winUnknownLifeTime    = 0xFFFFFFFF
)

// windowsPowerStatus is the portable view of SYSTEM_POWER_STATUS.
type windowsPowerStatus struct {
	ACLineStatus       byte
	BatteryFlag        byte
	BatteryLifePercent byte
	BatteryLifeTime    uint32
}

// batteryFromWindowsPower maps SYSTEM_POWER_STATUS to the shared battery row.
// It reports false for desktops without a system battery and for readings the
// firmware marks unknown.
func batteryFromWindowsPower(ps windowsPowerStatus) (BatteryStatus, bool) {
	if ps.BatteryFlag == winBatteryFlagUnknown || ps.BatteryFlag&winBatteryNoSystem != 0 {
		return BatteryStatus{}, false
	}
	if ps.BatteryLifePercent == winUnknownPercent {
		return BatteryStatus{}, false
	}

	percent := float64(ps.BatteryLifePercent)
	status := "discharging"
	switch {
	case ps.BatteryFlag&winBatteryCharging != 0:
		status = "charging"
	case ps.ACLineStatus == winACOnline && percent >= 100:
		status = "charged"
	case ps.ACLineStatus == winACOnline:
		status = "AC"
	}

	timeLeft := ""
	if status == "discharging" && ps.BatteryLifeTime != winUnknownLifeTime {
		timeLeft = formatBatteryMinutes(ps.BatteryLifeTime / 60)
	}

	return BatteryStatus{Percent: percent, Status: status, TimeLeft: timeLeft}, true
}

// formatBatteryMinutes renders minutes the way pmset does ("3:07").
func formatBatteryMinutes(minutes uint32) string {
	return fmt.Sprintf("%d:%02d", minutes/60, minutes%60)
}

// batteryCapacityPercent turns WMI charge capacities into the "maximum
// capacity" percentage macOS reports. Out-of-range readings yield 0 (unknown).
func batteryCapacityPercent(fullCharged, designed uint32) int {
	if fullCharged == 0 || designed == 0 {
		return 0
	}
	pct := int(float64(fullCharged)*100/float64(designed) + 0.5)
	if pct <= 0 || pct > 150 {
		return 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// windowsOSLabel builds "Windows 11 Pro 24H2 (26100)". ProductName still says
// "Windows 10" on Windows 11 because Microsoft never updated that value, so
// the build number decides the major version.
func windowsOSLabel(productName, displayVersion, build string) string {
	name := strings.TrimSpace(productName)
	if name == "" {
		name = "Windows"
	}
	if buildNumber := parseInt(build); buildNumber >= 22000 && strings.HasPrefix(name, "Windows 10") {
		name = "Windows 11" + strings.TrimPrefix(name, "Windows 10")
	}
	label := name
	if v := strings.TrimSpace(displayVersion); v != "" {
		label += " " + v
	}
	if b := strings.TrimSpace(build); b != "" {
		label += " (" + b + ")"
	}
	return label
}

// windowsModelLabel joins BIOS manufacturer and product, skipping OEM filler.
func windowsModelLabel(manufacturer, product string) string {
	isFiller := func(s string) bool {
		lower := strings.ToLower(strings.TrimSpace(s))
		return lower == "" || lower == "system manufacturer" || lower == "system product name" ||
			lower == "to be filled by o.e.m." || lower == "default string"
	}
	var parts []string
	if !isFiller(manufacturer) {
		parts = append(parts, strings.TrimSpace(manufacturer))
	}
	if !isFiller(product) {
		p := strings.TrimSpace(product)
		if len(parts) == 0 || !strings.HasPrefix(strings.ToLower(p), strings.ToLower(parts[0])) {
			parts = append(parts, p)
		} else {
			parts = []string{p}
		}
	}
	if len(parts) == 0 {
		return "Unknown"
	}
	return strings.Join(parts, " ")
}

// proxyFromWindowsSettings reads the WinINet proxy values stored under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings.
// ProxyServer is either "host:port" or per-scheme "http=h:p;https=h:p;socks=h:p".
func proxyFromWindowsSettings(proxyEnable uint64, proxyServer, autoConfigURL string) ProxyStatus {
	if proxyEnable != 0 {
		server := strings.TrimSpace(proxyServer)
		if server != "" {
			if !strings.Contains(server, "=") {
				if host := parseProxyHost(server); host != "" {
					return ProxyStatus{Enabled: true, Type: "HTTP", Host: host}
				}
			} else {
				schemes := map[string]string{}
				for part := range strings.SplitSeq(server, ";") {
					key, value, ok := strings.Cut(part, "=")
					if !ok {
						continue
					}
					schemes[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
				}
				for _, pick := range []struct{ key, label string }{
					{"https", "HTTPS"}, {"http", "HTTP"}, {"socks", "SOCKS"},
				} {
					if host := parseProxyHost(schemes[pick.key]); host != "" {
						return ProxyStatus{Enabled: true, Type: pick.label, Host: host}
					}
				}
			}
		}
	}
	if pac := strings.TrimSpace(autoConfigURL); pac != "" {
		return ProxyStatus{Enabled: true, Type: "PAC", Host: parseProxyHost(pac)}
	}
	return ProxyStatus{}
}

// processCPUTracker turns cumulative per-process CPU time into a percentage
// between two samples, the way Task Manager does. 100% is one full core, the
// same scale ps(1) uses on macOS.
type processCPUTracker struct {
	prev     map[processKey]int64 // cumulative CPU time in 100ns units
	prevWall int64                // wall clock in 100ns units
}

type processKey struct {
	pid        int
	createTime int64
}

// update records a new sample and returns each process's CPU percent since the
// previous one. With no earlier sample, the percent is lifetime CPU time over
// process age, which is what ps reports for a process it sees the first time.
func (t *processCPUTracker) update(nowWall int64, samples map[processKey]int64) map[processKey]float64 {
	out := make(map[processKey]float64, len(samples))
	elapsed := nowWall - t.prevWall
	for key, cpu := range samples {
		if prevCPU, ok := t.prev[key]; ok && t.prevWall > 0 && elapsed > 0 {
			delta := max(cpu-prevCPU, 0)
			out[key] = float64(delta) * 100 / float64(elapsed)
			continue
		}
		if age := nowWall - key.createTime; key.createTime > 0 && age > 0 {
			out[key] = float64(cpu) * 100 / float64(age)
		}
	}
	t.prev = samples
	t.prevWall = nowWall
	return out
}
