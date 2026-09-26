//go:build windows

package clean

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProfile struct {
	local, temp string
}

// newFakeProfile points LOCALAPPDATA and TEMP at a temp tree and fills it with
// targets plus sibling data that must survive.
func newFakeProfile(t *testing.T) fakeProfile {
	t.Helper()
	root := t.TempDir()
	p := fakeProfile{local: filepath.Join(root, "Local"), temp: filepath.Join(root, "Local", "Temp")}
	t.Setenv("LOCALAPPDATA", p.local)
	t.Setenv("TEMP", p.temp)
	t.Setenv("TMP", p.temp)
	t.Setenv("MO_NO_OPLOG", "1")

	old := time.Now().Add(-48 * time.Hour)
	write := func(rel string, size int, mod time.Time) {
		path := filepath.Join(p.local, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write(`Temp\old.tmp`, 100, old)
	write(`Temp\sub\old2.tmp`, 50, old)
	write(`Temp\fresh.tmp`, 10, time.Now())
	write(`Google\Chrome\User Data\Default\Cache\Cache_Data\f_000001`, 1000, time.Now())
	write(`Google\Chrome\User Data\Default\Cookies`, 7, time.Now())
	write(`Google\Chrome\User Data\Default\Local Storage\leveldb\000003.log`, 7, time.Now())
	write(`npm-cache\_cacache\content-v2\sha512\ab\cd`, 300, time.Now())
	write(`npm-cache\_logs\debug.log`, 5, time.Now())

	processStateFunc = func([]string) processState { return processNotRunning }
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() {
		processStateFunc = runningProcessState
		stdinIsTerminal = func() bool { return true }
	})
	return p
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCleanDryRunChangesNothing(t *testing.T) {
	p := newFakeProfile(t)
	code, out, _ := run(t, "", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, rel := range []string{`Temp\old.tmp`, `Google\Chrome\User Data\Default\Cache\Cache_Data\f_000001`, `npm-cache\_cacache\content-v2\sha512\ab\cd`} {
		if !exists(filepath.Join(p.local, rel)) {
			t.Fatalf("dry run removed %s", rel)
		}
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "Chrome Cache (Default)") {
		t.Fatalf("preview missing expected rows:\n%s", out)
	}
}

func TestCleanRemovesTargetsAndKeepsEverythingElse(t *testing.T) {
	p := newFakeProfile(t)
	code, out, errOut := run(t, "y\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, rel := range []string{`Temp\old.tmp`, `Temp\sub\old2.tmp`, `Google\Chrome\User Data\Default\Cache\Cache_Data\f_000001`, `npm-cache\_cacache\content-v2\sha512\ab\cd`} {
		if exists(filepath.Join(p.local, rel)) {
			t.Fatalf("%s should have been removed\n%s", rel, out)
		}
	}
	for _, rel := range []string{
		`Temp\fresh.tmp`,                                                      // younger than a day
		`Temp`, `Google\Chrome\User Data\Default\Cache`, `npm-cache\_cacache`, // roots stay
		`Google\Chrome\User Data\Default\Cookies`,
		`Google\Chrome\User Data\Default\Local Storage\leveldb\000003.log`,
		`npm-cache\_logs\debug.log`,
	} {
		if !exists(filepath.Join(p.local, rel)) {
			t.Fatalf("%s must be kept\n%s", rel, out)
		}
	}
	if exists(filepath.Join(p.local, `Temp\sub`)) {
		t.Fatal("empty subdirectory under Temp should be removed")
	}
	if !strings.Contains(out, "Freed 1.4 kB (4 files removed)") {
		t.Fatalf("summary should report the bytes actually freed:\n%s", out)
	}
}

func TestCleanDeclineDeletesNothing(t *testing.T) {
	p := newFakeProfile(t)
	if code, out, _ := run(t, "n\n"); code != 0 || !strings.Contains(out, "Cancelled") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if !exists(filepath.Join(p.local, `Temp\old.tmp`)) {
		t.Fatal("declining must delete nothing")
	}
}

func TestCleanNonInteractiveRequiresYes(t *testing.T) {
	p := newFakeProfile(t)
	stdinIsTerminal = func() bool { return false }
	code, _, errOut := run(t, "y\n")
	if code != 1 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d, stderr %q; want refusal naming --yes", code, errOut)
	}
	if !exists(filepath.Join(p.local, `Temp\old.tmp`)) {
		t.Fatal("refused run deleted files")
	}
	if code, _, _ := run(t, "", "--yes"); code != 0 || exists(filepath.Join(p.local, `Temp\old.tmp`)) {
		t.Fatalf("--yes run: exit %d, file still present %v", code, exists(filepath.Join(p.local, `Temp\old.tmp`)))
	}
}

func TestCleanSkipsBrowserWhenRunningOrUnknown(t *testing.T) {
	for _, state := range []processState{processRunning, processUnknown} {
		p := newFakeProfile(t)
		processStateFunc = func([]string) processState { return state }
		code, out, _ := run(t, "", "--yes")
		cache := filepath.Join(p.local, `Google\Chrome\User Data\Default\Cache\Cache_Data\f_000001`)
		if code != 0 || !exists(cache) {
			t.Fatalf("state %d: browser cache removed or failure (exit %d)\n%s", state, code, out)
		}
		if !strings.Contains(out, "skipped:") {
			t.Fatalf("state %d: preview should say why the target was skipped:\n%s", state, out)
		}
	}
}

func TestCleanKeepsFilesChangedAfterScan(t *testing.T) {
	p := newFakeProfile(t)
	plans := scanTargets(context.Background(), cleanTargets())
	changed := filepath.Join(p.local, `npm-cache\_cacache\content-v2\sha512\ab\cd`)
	if err := os.WriteFile(changed, make([]byte, 999), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range plans {
		executePlan(&plans[i], &opLog{})
	}
	if !exists(changed) {
		t.Fatal("a file rewritten after the scan must be kept")
	}
}

func TestCleanDoesNotFollowLinksInsideTargets(t *testing.T) {
	p := newFakeProfile(t)
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(outside, "doc.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	_ = os.Chtimes(keep, old, old)
	link := filepath.Join(p.temp, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code, _, _ := run(t, "", "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !exists(keep) {
		t.Fatal("clean followed a link out of the target and deleted its contents")
	}
}

func TestCleanTargetsNeverIncludeProtectedOrUpdateTrees(t *testing.T) {
	newFakeProfile(t)
	for _, tg := range cleanTargets() {
		lower := strings.ToLower(tg.root)
		if sys := os.Getenv("SystemRoot"); sys != "" && pathWithinFold(tg.root, sys) {
			t.Fatalf("target %q (%s) is under %%SystemRoot%%", tg.name, tg.root)
		}
		for _, banned := range []string{"softwaredistribution", "$recycle.bin", "onedrive", `\packages`, "cookies", "local storage"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("target %q (%s) touches a banned tree %q", tg.name, tg.root, banned)
			}
		}
	}
}
