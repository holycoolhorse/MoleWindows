package uninstall

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilterAppsDropsComponentsUpdatesAndDuplicates(t *testing.T) {
	apps := filterApps([]App{
		{Key: "a", Name: "Zeta Editor", UninstallString: "x.exe"},
		{Key: "b", Name: "Hidden", UninstallString: "x.exe", systemComponent: true},
		{Key: "c", Name: "KB123 Update", UninstallString: "x.exe", parentKey: "Office"},
		{Key: "d", Name: "Security Update", UninstallString: "x.exe", releaseType: "Security Update"},
		{Key: "e", Name: "No Uninstaller"},
		{Key: "f", Name: "  ", UninstallString: "x.exe"},
		{Key: "g", Name: "Alpha Tool", Version: "1", UninstallString: "x.exe"},
		{Key: "h", Name: "alpha tool", Version: "1", UninstallString: "y.exe"},
		{Key: "i", Name: "NVIDIA Graphics Driver 551.23", UninstallString: "x.exe"},
	})
	var names []string
	for _, a := range apps {
		names = append(names, a.Name)
	}
	if want := []string{"Alpha Tool", "NVIDIA Graphics Driver 551.23", "Zeta Editor"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("filterApps = %v, want %v", names, want)
	}
	if !apps[1].Protected || apps[0].Protected || apps[2].Protected {
		t.Fatalf("only the driver should be protected: %+v", apps)
	}
}

func TestIsProtectedApp(t *testing.T) {
	for name, want := range map[string]bool{
		"Microsoft Visual C++ 2015-2022 Redistributable (x64)": true,
		"Microsoft Edge WebView2 Runtime":                      true,
		"Microsoft .NET Runtime - 8.0.4 (x64)":                 true,
		"Realtek High Definition Audio Driver":                 true,
		"CrowdStrike Windows Sensor":                           true,
		"Visual Studio Code":                                   false,
		"7-Zip 23.01 (x64)":                                    false,
		"Mozilla Firefox (x64 en-US)":                          false,
	} {
		if got := isProtectedApp(App{Name: name}); got != want {
			t.Errorf("isProtectedApp(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestUninstallCommand(t *testing.T) {
	tests := []struct {
		in, program, cmdline string
		wantErr              bool
	}{
		{`MsiExec.exe /I{12345678-1234-1234-1234-1234567890AB}`, `C:\Windows/System32/msiexec.exe`, ``, false},
		{`"C:\Program Files\App\unins000.exe" /SILENT`, `C:\Program Files\App\unins000.exe`, `"C:\Program Files\App\unins000.exe" /SILENT`, false},
		{`C:\Program Files\App\uninstall.exe --remove`, `C:\Program Files\App\uninstall.exe`, `"C:\Program Files\App\uninstall.exe" --remove`, false},
		{`MsiExec.exe /X`, "", "", true},
		{`"C:\broken.exe`, "", "", true},
		{`rundll32 something`, "", "", true},
		{``, "", "", true},
	}
	for _, tt := range tests {
		program, cmdline, err := uninstallCommand(tt.in, `C:\Windows`)
		if (err != nil) != tt.wantErr {
			t.Fatalf("uninstallCommand(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if tt.wantErr {
			continue
		}
		if filepath.ToSlash(program) != filepath.ToSlash(tt.program) {
			t.Errorf("uninstallCommand(%q) program = %q, want %q", tt.in, program, tt.program)
		}
		if tt.cmdline != "" && cmdline != tt.cmdline {
			t.Errorf("uninstallCommand(%q) cmdline = %q, want %q", tt.in, cmdline, tt.cmdline)
		}
	}
	// MSI entries always uninstall (/x), even when registered as /I.
	_, cmdline, _ := uninstallCommand(`MsiExec.exe /I{12345678-1234-1234-1234-1234567890ab}`, `C:\Windows`)
	if want := ` /x {12345678-1234-1234-1234-1234567890AB}`; len(cmdline) < len(want) || cmdline[len(cmdline)-len(want):] != want {
		t.Fatalf("msi cmdline = %q, want suffix %q", cmdline, want)
	}
}

func TestNameVariantsRejectGenericAndShortNames(t *testing.T) {
	got := nameVariants(App{Name: "7-Zip 23.01 (x64)", Publisher: "Igor Pavlov", InstallLocation: `C:\Program Files\7-Zip\`})
	if want := []string{"7-Zip 23.01 (x64)", "7-Zip"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nameVariants = %v, want %v", got, want)
	}
	for _, a := range []App{
		{Name: "App"}, {Name: "Tools"}, {Name: "Git", InstallLocation: `C:\Program Files\Git`},
		{Name: "Updater 2.0"}, {Name: "Microsoft"},
	} {
		if got := nameVariants(a); len(got) != 0 {
			t.Errorf("nameVariants(%q) = %v, want none (generic or too short)", a.Name, got)
		}
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindLeftoversExactNamesOnly(t *testing.T) {
	root := t.TempDir()
	roots := leftoverRoots{
		AppData:      filepath.Join(root, "Roaming"),
		LocalAppData: filepath.Join(root, "Local"),
		ProgramData:  filepath.Join(root, "ProgramData"),
	}
	mkdirs(t,
		filepath.Join(roots.AppData, "Fooview"),
		filepath.Join(roots.LocalAppData, "FOOVIEW"),
		filepath.Join(roots.LocalAppData, "Acme Corp", "Fooview"),
		filepath.Join(roots.LocalAppData, "Programs", "Fooview"),
		filepath.Join(roots.AppData, "FooviewPlus"),        // similar name: never
		filepath.Join(roots.AppData, "Acme Corp", "Other"), // same vendor: never
		filepath.Join(roots.AppData, "Acme Corp", "Fooview Tools"),
	)
	app := App{Key: "k1", Name: "Fooview 3.2", Publisher: "Acme Corp"}
	got := findLeftovers(app, nil, roots)
	want := map[string]bool{
		filepath.Join(roots.AppData, "Fooview"):                   true,
		filepath.Join(roots.LocalAppData, "FOOVIEW"):              true,
		filepath.Join(roots.LocalAppData, "Acme Corp", "Fooview"): true,
		filepath.Join(roots.LocalAppData, "Programs", "Fooview"):  true,
	}
	if len(got) != len(want) {
		t.Fatalf("findLeftovers = %+v, want %d exact matches", got, len(want))
	}
	for _, l := range got {
		if !want[l.Path] {
			t.Fatalf("unexpected leftover %s", l.Path)
		}
	}
}

func TestFindLeftoversKeepsFoldersClaimedByInstalledApps(t *testing.T) {
	root := t.TempDir()
	roots := leftoverRoots{AppData: filepath.Join(root, "Roaming"), LocalAppData: filepath.Join(root, "Local")}
	shared := filepath.Join(roots.AppData, "Fooview")
	mkdirs(t, shared, filepath.Join(roots.LocalAppData, "Fooview"))

	removed := App{Key: "old", Name: "Fooview"}
	// Another installed edition uses the same folder name.
	sibling := App{Key: "new", Name: "Fooview", Version: "4"}
	if got := findLeftovers(removed, []App{removed, sibling}, roots); len(got) != 0 {
		t.Fatalf("folders claimed by a still-installed app must be kept, got %+v", got)
	}

	// A still-installed app living inside the candidate folder also keeps it.
	other := App{Key: "x", Name: "Unrelated Plugin", InstallLocation: filepath.Join(shared, "plugins", "x")}
	for _, l := range findLeftovers(removed, []App{other}, roots) {
		if l.Path == shared {
			t.Fatal("a folder holding another app's install location must be kept")
		}
	}
}

func TestFindLeftoversInstallLocationNeedsExactName(t *testing.T) {
	root := t.TempDir()
	games := filepath.Join(root, "Games")
	own := filepath.Join(root, "Fooview")
	mkdirs(t, games, own)
	if got := findLeftovers(App{Key: "a", Name: "Fooview", InstallLocation: games}, nil, leftoverRoots{}); len(got) != 0 {
		t.Fatalf("a shared install parent must never be offered, got %+v", got)
	}
	got := findLeftovers(App{Key: "a", Name: "Fooview", InstallLocation: own}, nil, leftoverRoots{})
	if len(got) != 1 || got[0].Path != own {
		t.Fatalf("own install folder = %+v, want %s", got, own)
	}
}

func TestMatchApps(t *testing.T) {
	apps := []App{{Name: "Visual Studio Code"}, {Name: "Visual Studio 2022"}, {Name: "Code"}}
	if got := matchApps(apps, "code"); len(got) != 1 || got[0].Name != "Code" {
		t.Fatalf("exact name should win, got %+v", got)
	}
	if got := matchApps(apps, "visual studio"); len(got) != 2 {
		t.Fatalf("partial match = %+v, want both Visual Studio entries", got)
	}
}
