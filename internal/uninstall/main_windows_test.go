//go:build windows

package uninstall

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSystem struct {
	installed []App
	ran       []string
	recycled  []string
	// removeOnRun drops the app from the registry when its uninstaller runs.
	removeOnRun bool
	runErr      error
}

func setupFake(t *testing.T, apps ...App) (*fakeSystem, leftoverRoots) {
	t.Helper()
	root := t.TempDir()
	roots := leftoverRoots{
		AppData:      filepath.Join(root, "Roaming"),
		LocalAppData: filepath.Join(root, "Local"),
		ProgramData:  filepath.Join(root, "ProgramData"),
	}
	t.Setenv("APPDATA", roots.AppData)
	t.Setenv("LOCALAPPDATA", roots.LocalAppData)
	t.Setenv("ProgramData", roots.ProgramData)
	t.Setenv("MO_NO_OPLOG", "1")

	fs := &fakeSystem{installed: apps, removeOnRun: true}
	origRead, origRun, origRecycle, origProt, origTerm := readAppsFunc, runUninstallFunc, recycleFunc, isProtectedPath, stdinIsTerminal
	readAppsFunc = func() []App { return append([]App(nil), fs.installed...) }
	runUninstallFunc = func(program, cmdline string) error {
		fs.ran = append(fs.ran, cmdline)
		if fs.runErr != nil {
			return fs.runErr
		}
		if fs.removeOnRun {
			fs.installed = fs.installed[1:]
		}
		return nil
	}
	recycleFunc = func(path string) error {
		fs.recycled = append(fs.recycled, path)
		return os.RemoveAll(path)
	}
	isProtectedPath = func(string) bool { return false }
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() {
		readAppsFunc, runUninstallFunc, recycleFunc, isProtectedPath, stdinIsTerminal = origRead, origRun, origRecycle, origProt, origTerm
	})
	return fs, roots
}

func fooview() App {
	return App{Key: `HKCU\x\Fooview`, Name: "Fooview", Version: "3.2", Publisher: "Acme Corp", UninstallString: `"C:\Program Files\Fooview\unins000.exe"`}
}

func runMain(stdin string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUninstallRunsUninstallerThenRecyclesExactLeftovers(t *testing.T) {
	fs, roots := setupFake(t, fooview())
	leftover := filepath.Join(roots.AppData, "Fooview")
	unrelated := filepath.Join(roots.AppData, "FooviewPlus")
	mkdirs(t, leftover, unrelated)

	code, out, errOut := runMain("y\ny\n", "fooview")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	if len(fs.ran) != 1 || !strings.Contains(fs.ran[0], "unins000.exe") {
		t.Fatalf("uninstaller runs = %v", fs.ran)
	}
	if len(fs.recycled) != 1 || fs.recycled[0] != leftover {
		t.Fatalf("recycled = %v, want only %s", fs.recycled, leftover)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("a similarly named folder must be kept")
	}
}

func TestUninstallDryRunRunsNothing(t *testing.T) {
	fs, roots := setupFake(t, fooview())
	mkdirs(t, filepath.Join(roots.AppData, "Fooview"))
	code, out, _ := runMain("", "Fooview", "--dry-run")
	if code != 0 || len(fs.ran) != 0 || len(fs.recycled) != 0 {
		t.Fatalf("dry run: exit %d ran %v recycled %v", code, fs.ran, fs.recycled)
	}
	if !strings.Contains(out, "Leftovers offered") || !strings.Contains(out, "unins000.exe") {
		t.Fatalf("dry run should preview uninstaller and leftovers:\n%s", out)
	}
}

func TestUninstallKeepsLeftoversWhenAppStillInstalled(t *testing.T) {
	fs, roots := setupFake(t, fooview())
	fs.removeOnRun = false // the user cancelled the uninstaller UI
	mkdirs(t, filepath.Join(roots.AppData, "Fooview"))
	code, _, errOut := runMain("y\ny\n", "Fooview")
	if code != 1 || !strings.Contains(errOut, "still installed") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if len(fs.recycled) != 0 {
		t.Fatalf("leftovers of an app that is still installed were recycled: %v", fs.recycled)
	}
}

func TestUninstallKeepsLeftoversWhenUninstallerFails(t *testing.T) {
	fs, roots := setupFake(t, fooview())
	fs.runErr = errors.New("uninstaller exited with code 2")
	mkdirs(t, filepath.Join(roots.AppData, "Fooview"))
	if code, _, _ := runMain("y\ny\n", "Fooview"); code != 1 || len(fs.recycled) != 0 {
		t.Fatalf("exit %d, recycled %v", code, fs.recycled)
	}
}

func TestUninstallRefusesProtectedApps(t *testing.T) {
	fs, _ := setupFake(t, App{Key: "k", Name: "Microsoft Visual C++ 2015-2022 Redistributable (x64)", UninstallString: `"C:\x\setup.exe"`})
	code, _, errOut := runMain("y\n", "Visual C++", "--yes")
	if code != 1 || len(fs.ran) != 0 || !strings.Contains(errOut, "will not uninstall") {
		t.Fatalf("exit %d ran %v stderr %q", code, fs.ran, errOut)
	}
}

func TestUninstallDeclineChangesNothing(t *testing.T) {
	fs, _ := setupFake(t, fooview())
	if code, out, _ := runMain("n\n", "Fooview"); code != 0 || len(fs.ran) != 0 || !strings.Contains(out, "Cancelled") {
		t.Fatalf("exit %d ran %v\n%s", code, fs.ran, out)
	}
}

func TestUninstallNonInteractiveNeedsYes(t *testing.T) {
	fs, _ := setupFake(t, fooview())
	stdinIsTerminal = func() bool { return false }
	if code, _, errOut := runMain("", "Fooview"); code != 1 || len(fs.ran) != 0 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d ran %v stderr %q", code, fs.ran, errOut)
	}
	if code, _, _ := runMain("", "Fooview", "--yes"); code != 0 || len(fs.ran) != 1 {
		t.Fatalf("--yes run: exit %d ran %v", code, fs.ran)
	}
}

func TestUninstallAmbiguousNameStops(t *testing.T) {
	fs, _ := setupFake(t,
		App{Key: "a", Name: "Fooview Pro", UninstallString: `"C:\a.exe"`},
		App{Key: "b", Name: "Fooview Lite", UninstallString: `"C:\b.exe"`})
	if code, _, errOut := runMain("", "fooview", "--yes"); code != 1 || len(fs.ran) != 0 || !strings.Contains(errOut, "use the full name") {
		t.Fatalf("exit %d ran %v stderr %q", code, fs.ran, errOut)
	}
}

func TestUninstallListJSON(t *testing.T) {
	setupFake(t, fooview())
	code, out, _ := runMain("", "--list", "--json")
	if code != 0 || !strings.Contains(out, `"name": "Fooview"`) || strings.Contains(out, "unins000") {
		t.Fatalf("exit %d, list JSON:\n%s", code, out)
	}
}

// The real registry reader must at least run; CI machines have entries.
func TestReadInstalledAppsRuns(t *testing.T) {
	for _, a := range filterApps(readInstalledApps()) {
		if a.Name == "" || a.Key == "" {
			t.Fatalf("filtered entry without name or key: %+v", a)
		}
	}
}
