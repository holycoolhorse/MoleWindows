package status

import "testing"

func TestBatteryFromWindowsPower(t *testing.T) {
	tests := []struct {
		name   string
		in     windowsPowerStatus
		wantOK bool
		want   BatteryStatus
	}{
		{"desktop without battery", windowsPowerStatus{ACLineStatus: 1, BatteryFlag: 128, BatteryLifePercent: 255}, false, BatteryStatus{}},
		{"unknown flag", windowsPowerStatus{BatteryFlag: 255, BatteryLifePercent: 50}, false, BatteryStatus{}},
		{"unknown percent", windowsPowerStatus{BatteryFlag: 1, BatteryLifePercent: 255}, false, BatteryStatus{}},
		{"charging", windowsPowerStatus{ACLineStatus: 1, BatteryFlag: 8, BatteryLifePercent: 42, BatteryLifeTime: winUnknownLifeTime},
			true, BatteryStatus{Percent: 42, Status: "charging"}},
		{"full on AC", windowsPowerStatus{ACLineStatus: 1, BatteryFlag: 1, BatteryLifePercent: 100, BatteryLifeTime: winUnknownLifeTime},
			true, BatteryStatus{Percent: 100, Status: "charged"}},
		{"AC not charging", windowsPowerStatus{ACLineStatus: 1, BatteryFlag: 1, BatteryLifePercent: 80, BatteryLifeTime: winUnknownLifeTime},
			true, BatteryStatus{Percent: 80, Status: "AC"}},
		{"discharging with estimate", windowsPowerStatus{ACLineStatus: 0, BatteryFlag: 2, BatteryLifePercent: 30, BatteryLifeTime: 3*3600 + 7*60 + 59},
			true, BatteryStatus{Percent: 30, Status: "discharging", TimeLeft: "3:07"}},
		{"discharging without estimate", windowsPowerStatus{ACLineStatus: 0, BatteryFlag: 2, BatteryLifePercent: 30, BatteryLifeTime: winUnknownLifeTime},
			true, BatteryStatus{Percent: 30, Status: "discharging"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := batteryFromWindowsPower(tt.in)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("batteryFromWindowsPower(%+v) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestBatteryCapacityPercent(t *testing.T) {
	tests := []struct {
		full, design uint32
		want         int
	}{
		{0, 50000, 0},
		{45000, 0, 0},
		{42500, 50000, 85},
		{50500, 50000, 100}, // new cells can exceed design slightly
		{90000, 50000, 0},   // implausible reading
	}
	for _, tt := range tests {
		if got := batteryCapacityPercent(tt.full, tt.design); got != tt.want {
			t.Errorf("batteryCapacityPercent(%d, %d) = %d, want %d", tt.full, tt.design, got, tt.want)
		}
	}
}

func TestWindowsOSLabel(t *testing.T) {
	tests := []struct {
		product, display, build, want string
	}{
		{"Windows 10 Pro", "24H2", "26100", "Windows 11 Pro 24H2 (26100)"},
		{"Windows 10 Home", "22H2", "19045", "Windows 10 Home 22H2 (19045)"},
		{"Windows Server 2022 Standard", "21H2", "20348", "Windows Server 2022 Standard 21H2 (20348)"},
		{"", "", "", "Windows"},
	}
	for _, tt := range tests {
		if got := windowsOSLabel(tt.product, tt.display, tt.build); got != tt.want {
			t.Errorf("windowsOSLabel(%q, %q, %q) = %q, want %q", tt.product, tt.display, tt.build, got, tt.want)
		}
	}
}

func TestWindowsModelLabel(t *testing.T) {
	tests := []struct {
		manufacturer, product, want string
	}{
		{"LENOVO", "20XW0055US", "LENOVO 20XW0055US"},
		{"Dell Inc.", "Dell Inc. XPS 13 9310", "Dell Inc. XPS 13 9310"},
		{"System manufacturer", "System Product Name", "Unknown"},
		{"To Be Filled By O.E.M.", "B550M", "B550M"},
		{"", "", "Unknown"},
	}
	for _, tt := range tests {
		if got := windowsModelLabel(tt.manufacturer, tt.product); got != tt.want {
			t.Errorf("windowsModelLabel(%q, %q) = %q, want %q", tt.manufacturer, tt.product, got, tt.want)
		}
	}
}

func TestProxyFromWindowsSettings(t *testing.T) {
	tests := []struct {
		name   string
		enable uint64
		server string
		pac    string
		want   ProxyStatus
	}{
		{"disabled", 0, "proxy:8080", "", ProxyStatus{}},
		{"single server", 1, "127.0.0.1:7890", "", ProxyStatus{Enabled: true, Type: "HTTP", Host: "127.0.0.1:7890"}},
		{"per scheme prefers https", 1, "http=h1:80;https=h2:443;socks=h3:1080", "", ProxyStatus{Enabled: true, Type: "HTTPS", Host: "h2:443"}},
		{"socks only", 1, "socks=127.0.0.1:1080", "", ProxyStatus{Enabled: true, Type: "SOCKS", Host: "127.0.0.1:1080"}},
		{"enabled but empty falls to PAC", 1, "", "http://wpad/proxy.pac", ProxyStatus{Enabled: true, Type: "PAC", Host: "wpad"}},
		{"PAC only", 0, "", "http://corp.example/proxy.pac", ProxyStatus{Enabled: true, Type: "PAC", Host: "corp.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := proxyFromWindowsSettings(tt.enable, tt.server, tt.pac); got != tt.want {
				t.Fatalf("proxyFromWindowsSettings() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestProcessCPUTracker(t *testing.T) {
	var tracker processCPUTracker
	key := processKey{pid: 42, createTime: 1_000}

	// First sample: lifetime average. 500 units of CPU over 1000 units of age.
	first := tracker.update(2_000, map[processKey]int64{key: 500})
	if got := first[key]; got != 50 {
		t.Fatalf("first sample = %v, want 50", got)
	}

	// Second sample: delta only. 2000 CPU units over 1000 wall units = 2 cores.
	second := tracker.update(3_000, map[processKey]int64{key: 2_500})
	if got := second[key]; got != 200 {
		t.Fatalf("second sample = %v, want 200", got)
	}

	// A recycled PID has a different create time and must not reuse history.
	reused := processKey{pid: 42, createTime: 2_900}
	third := tracker.update(4_000, map[processKey]int64{reused: 550})
	if got := third[reused]; got != 50 {
		t.Fatalf("recycled pid sample = %v, want 50", got)
	}
}
