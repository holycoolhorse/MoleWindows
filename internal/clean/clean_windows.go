//go:build windows

// Package clean implements `mole clean` on Windows: it removes rebuildable
// user-scope caches and old temp files after a preview. The preview and the
// real run work from one scan plan, every file is revalidated at the sink,
// reparse points are never followed or deleted, and roots are never removed.
package clean

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tw93/mole/internal/analyze"
	"github.com/tw93/mole/internal/units"
)

const usageText = `Usage: mole clean [OPTIONS]

Remove rebuildable caches and temp files older than a day from your user
profile. Shows a preview first and deletes only after you confirm.

Options:
  --dry-run       Show what would be removed; change nothing
  --yes           Skip the confirmation (required when stdin is not a terminal)
  --debug         List files that could not be removed
  -h, --help      Show this help message
`

// Time budgets: a slow tree is skipped whole rather than half-deleted.
var (
	targetScanBudget = 30 * time.Second
	totalScanBudget  = 2 * time.Minute
)

// Seams replaced in tests.
var (
	processStateFunc = runningProcessState
	now              = time.Now
	stdinIsTerminal  = func() bool {
		var mode uint32
		return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
	}
)

type candidate struct {
	path    string
	size    int64
	modTime time.Time
}

type targetPlan struct {
	target
	files      []candidate
	dirs       []string // subdirectories, removed only if they end up empty
	bytes      int64
	skipReason string
	finalRoot  string // physical root, for containment checks at the sink
}

// Main runs `mole clean` and returns the process exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mole clean", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dryRun := flags.Bool("dry-run", false, "")
	yes := flags.Bool("yes", false, "")
	debug := flags.Bool("debug", false, "")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stdout, usageText)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err)
		_, _ = fmt.Fprintln(stderr, "Use 'mole clean --help' for usage information")
		return 1
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument: %s\nUse 'mole clean --help' for usage information\n", flags.Arg(0))
		return 1
	}
	if !*dryRun && !*yes && !stdinIsTerminal() {
		_, _ = fmt.Fprintln(stderr, "Refusing to clean without confirmation: stdin is not a terminal.")
		_, _ = fmt.Fprintln(stderr, "Run 'mole clean --dry-run' to preview, or 'mole clean --yes' to clean non-interactively.")
		return 1
	}

	plans := scanTargets(context.Background(), cleanTargets())
	printPreview(stdout, plans, *dryRun)
	total, count := planTotals(plans)
	if *dryRun {
		return 0
	}
	if count == 0 {
		_, _ = fmt.Fprintln(stdout, "Nothing to clean.")
		return 0
	}
	if !*yes {
		_, _ = fmt.Fprintf(stdout, "\nDelete %s in %s permanently? Type y and press Enter [y/N]: ", units.BytesSI(total), fileCount(count))
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			_, _ = fmt.Fprintln(stdout, "Cancelled. Nothing was deleted.")
			return 0
		}
	}

	log := openOpLog()
	defer log.close()
	var freed int64
	var removed, failed int
	var failures []string
	for i := range plans {
		p := &plans[i]
		if p.skipReason != "" {
			continue
		}
		r := executePlan(p, log)
		freed += r.freed
		removed += r.removed
		failed += len(r.failures)
		failures = append(failures, r.failures...)
	}

	_, _ = fmt.Fprintf(stdout, "\nFreed %s (%s removed).\n", units.BytesSI(freed), fileCount(removed))
	if failed > 0 {
		_, _ = fmt.Fprintf(stdout, "%s in use or changed since the scan; kept.\n", fileCount(failed))
		if *debug {
			for _, f := range failures {
				_, _ = fmt.Fprintf(stdout, "  kept: %s\n", f)
			}
		}
	}
	return 0
}

// scanTargets builds the one plan both the preview and the real run use.
func scanTargets(ctx context.Context, targets []target) []targetPlan {
	ctx, cancel := context.WithTimeout(ctx, totalScanBudget)
	defer cancel()
	plans := make([]targetPlan, 0, len(targets))
	for _, t := range targets {
		plans = append(plans, scanTarget(ctx, t))
	}
	return plans
}

func scanTarget(parent context.Context, t target) targetPlan {
	p := targetPlan{target: t}
	info, err := os.Lstat(t.root)
	if err != nil {
		p.skipReason = "not found"
		return p
	}
	if !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		p.skipReason = "not a plain folder; kept"
		return p
	}
	if analyze.IsProtectedPath(filepath.Join(t.root, "probe")) {
		p.skipReason = "protected location; kept"
		return p
	}
	if len(t.guard) > 0 {
		switch processStateFunc(t.guard) {
		case processRunning:
			p.skipReason = t.owner + " is running; close it and run again"
			return p
		case processUnknown:
			p.skipReason = "could not check whether " + t.owner + " is running; kept"
			return p
		}
	}
	finalRoot, err := finalPath(t.root)
	if err != nil {
		p.skipReason = "could not resolve folder; kept"
		return p
	}
	p.finalRoot = finalRoot

	ctx, cancel := context.WithTimeout(parent, targetScanBudget)
	defer cancel()
	cutoff := now().Add(-t.minAge)
	walkErr := filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == t.root {
			return nil
		}
		// Junctions report ModeIrregular, symlinks ModeSymlink: never follow,
		// never delete, never count.
		if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			p.dirs = append(p.dirs, path)
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if t.minAge > 0 && fi.ModTime().After(cutoff) {
			return nil
		}
		p.files = append(p.files, candidate{path: path, size: fi.Size(), modTime: fi.ModTime()})
		p.bytes += fi.Size()
		return nil
	})
	if walkErr != nil {
		// A partial scan never feeds the delete loop.
		p.files, p.dirs, p.bytes = nil, nil, 0
		p.skipReason = "scan took too long; skipped"
	}
	return p
}

type execResult struct {
	freed    int64
	removed  int
	failures []string
}

// executePlan deletes one target's files. Each file is revalidated right
// before removal: still a regular file (not a reparse point), unchanged since
// the scan, lexically and physically inside the root, and not protected.
func executePlan(p *targetPlan, log *opLog) execResult {
	var r execResult
	dirFinal := map[string]string{}
	inside := func(path string) bool {
		if !pathWithinFold(path, p.root) || pathEqualFold(path, p.root) {
			return false
		}
		dir := filepath.Dir(path)
		final, ok := dirFinal[dir]
		if !ok {
			var err error
			if final, err = finalPath(dir); err != nil {
				final = ""
			}
			dirFinal[dir] = final
		}
		return final != "" && pathWithinFold(final, p.finalRoot)
	}

	for _, c := range p.files {
		info, err := os.Lstat(c.path)
		switch {
		case err != nil:
			continue // already gone
		case !info.Mode().IsRegular(),
			info.Size() != c.size || !info.ModTime().Equal(c.modTime),
			!inside(c.path),
			analyze.IsProtectedPath(c.path):
			r.failures = append(r.failures, c.path)
			log.write("kept", c.path, c.size)
			continue
		}
		if err := os.Remove(c.path); err != nil {
			r.failures = append(r.failures, c.path)
			log.write("kept", c.path, c.size)
			continue
		}
		r.freed += c.size
		r.removed++
		log.write("deleted", c.path, c.size)
	}

	// Deepest first, so parents empty out; os.Remove refuses non-empty dirs.
	sort.Slice(p.dirs, func(i, j int) bool { return len(p.dirs[i]) > len(p.dirs[j]) })
	for _, dir := range p.dirs {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			continue
		}
		if !inside(dir) || analyze.IsProtectedPath(dir) {
			continue
		}
		_ = os.Remove(dir)
	}
	return r
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func planTotals(plans []targetPlan) (int64, int) {
	var total int64
	var count int
	for _, p := range plans {
		if p.skipReason == "" {
			total += p.bytes
			count += len(p.files)
		}
	}
	return total, count
}

func printPreview(w io.Writer, plans []targetPlan, dryRun bool) {
	title := "Mole clean - preview"
	if dryRun {
		title += " (dry run, nothing will be deleted)"
	}
	_, _ = fmt.Fprintf(w, "%s\n", title)
	section := ""
	for _, p := range plans {
		if p.skipReason == "not found" {
			continue
		}
		if p.skipReason == "" && len(p.files) == 0 {
			continue
		}
		if p.section != section {
			section = p.section
			_, _ = fmt.Fprintf(w, "\n%s\n", section)
		}
		if p.skipReason != "" {
			_, _ = fmt.Fprintf(w, "  %-44s %s\n", p.name, "skipped: "+p.skipReason)
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-44s %14s  %10s\n", p.name, fileCount(len(p.files)), units.BytesSI(p.bytes))
	}
	total, count := planTotals(plans)
	_, _ = fmt.Fprintf(w, "\nTotal: %s in %s\n", units.BytesSI(total), fileCount(count))
}

// ---- process guard ----

type processState int

const (
	processNotRunning processState = iota
	processRunning
	processUnknown
)

// runningProcessState reports whether any of images is running. A snapshot
// failure is processUnknown, which callers treat like running.
func runningProcessState(images []string) processState {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return processUnknown
	}
	defer windows.CloseHandle(snap) //nolint:errcheck // snapshot handle
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return processUnknown
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if slices.Contains(images, name) {
			return processRunning
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return processNotRunning
			}
			return processUnknown
		}
	}
}

// ---- paths ----

// finalPath follows every symlink, junction, and mount point in dir.
func finalPath(dir string) (string, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only query handle
	buf := make([]uint16, windows.MAX_LONG_PATH)
	for _, flags := range []uint32{0 /* VOLUME_NAME_DOS */, 1 /* VOLUME_NAME_GUID */} {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err == nil && n > 0 && int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
	}
	return "", errors.New("cannot resolve final path")
}

func pathEqualFold(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// pathWithinFold reports whether path is root or below it, case-insensitively.
func pathWithinFold(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if strings.EqualFold(path, root) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, `\`) {
		prefix += `\`
	}
	return len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
}

// ---- operation log ----

type opLog struct {
	w *bufio.Writer
	f *os.File
}

// openOpLog appends to %LOCALAPPDATA%\mole\logs\operations.log unless
// MO_NO_OPLOG=1. Logging problems never block cleanup.
func openOpLog() *opLog {
	if os.Getenv("MO_NO_OPLOG") == "1" {
		return &opLog{}
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "mole", "logs")
	if os.Getenv("LOCALAPPDATA") == "" || os.MkdirAll(dir, 0o755) != nil {
		return &opLog{}
	}
	f, err := os.OpenFile(filepath.Join(dir, "operations.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return &opLog{}
	}
	return &opLog{w: bufio.NewWriter(f), f: f}
}

func (l *opLog) write(action, path string, size int64) {
	if l.w == nil {
		return
	}
	_, _ = fmt.Fprintf(l.w, "%s\tclean\t%s\t%d\t%s\n", now().UTC().Format(time.RFC3339), action, size, path)
}

func (l *opLog) close() {
	if l.w == nil {
		return
	}
	_ = l.w.Flush()
	_ = l.f.Close()
}
