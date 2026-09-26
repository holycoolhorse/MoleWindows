//go:build windows

package uninstall

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/tw93/mole/internal/analyze"
	"github.com/tw93/mole/internal/oplog"
	"github.com/tw93/mole/internal/units"
)

const usageText = `Usage: mole uninstall [OPTIONS] [NAME]

Run an installed app's own uninstaller, then offer leftover folders that carry
the app's exact name for the Recycle Bin. Without NAME, pick from a list.

Options:
  --list          List installed apps and exit
  --json          With --list, print JSON
  --dry-run       Show the uninstaller and leftovers; change nothing
  --yes           Skip confirmations (required when stdin is not a terminal)
  -h, --help      Show this help message
`

// Seams replaced in tests.
var (
	readAppsFunc     = readInstalledApps
	runUninstallFunc = runUninstaller
	recycleFunc      = analyze.MoveToRecycleBin
	isProtectedPath  = analyze.IsProtectedPath
	stdinIsTerminal  = func() bool {
		var mode uint32
		return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
	}
)

func currentLeftoverRoots() leftoverRoots {
	return leftoverRoots{
		AppData:      os.Getenv("APPDATA"),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		ProgramData:  os.Getenv("ProgramData"),
	}
}

// Main runs `mole uninstall` and returns the process exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mole uninstall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	list := flags.Bool("list", false, "")
	asJSON := flags.Bool("json", false, "")
	dryRun := flags.Bool("dry-run", false, "")
	yes := flags.Bool("yes", false, "")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stdout, usageText)
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "%v\nUse 'mole uninstall --help' for usage information\n", err)
		return 1
	}
	in := bufio.NewReader(stdin)
	apps := filterApps(readAppsFunc())

	if *list {
		return printList(stdout, apps, *asJSON)
	}

	interactive := stdinIsTerminal()
	var app App
	switch query := strings.TrimSpace(strings.Join(flags.Args(), " ")); {
	case query != "":
		matches := matchApps(apps, query)
		switch len(matches) {
		case 0:
			_, _ = fmt.Fprintf(stderr, "No installed app matches %q.\nRun 'mole uninstall --list' to see installed apps.\n", query)
			return 1
		case 1:
			app = matches[0]
		default:
			_, _ = fmt.Fprintf(stderr, "%d installed apps match %q; use the full name:\n", len(matches), query)
			for _, m := range matches {
				_, _ = fmt.Fprintf(stderr, "  %s\n", m.Name)
			}
			return 1
		}
	case !interactive:
		_, _ = fmt.Fprintln(stderr, "Name the app to uninstall when stdin is not a terminal: mole uninstall <name> --yes")
		return 1
	default:
		var ok bool
		if app, ok = pickApp(stdout, in, apps); !ok {
			return 0
		}
	}

	if app.Protected {
		_, _ = fmt.Fprintf(stderr, "%s is a runtime, driver, or security component other software relies on; Mole will not uninstall it.\nUse Windows Settings > Apps if it really must go.\n", app.Name)
		return 1
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	program, cmdline, err := uninstallCommand(app.UninstallString, systemRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Cannot uninstall %s: %v\n", app.Name, err)
		return 1
	}

	roots := currentLeftoverRoots()
	preview := keepUnprotected(findLeftovers(app, apps, roots))
	_, _ = fmt.Fprintf(stdout, "%s %s\n", app.Name, app.Version)
	if app.Publisher != "" {
		_, _ = fmt.Fprintf(stdout, "  Publisher:   %s\n", app.Publisher)
	}
	_, _ = fmt.Fprintf(stdout, "  Uninstaller: %s\n", cmdline)
	if len(preview) > 0 {
		_, _ = fmt.Fprintln(stdout, "  Leftovers offered for the Recycle Bin afterwards:")
		for _, l := range preview {
			_, _ = fmt.Fprintf(stdout, "    %s (%s)\n", l.Path, l.Reason)
		}
	}
	if *dryRun {
		_, _ = fmt.Fprintln(stdout, "\nDry run: nothing was run or moved.")
		return 0
	}
	if !interactive && !*yes {
		_, _ = fmt.Fprintln(stderr, "Refusing to uninstall without confirmation: stdin is not a terminal.\nRun with --dry-run to preview, or --yes to proceed.")
		return 1
	}
	if !*yes && !confirm(stdout, in, "\nRun the uninstaller for "+app.Name+"?") {
		_, _ = fmt.Fprintln(stdout, "Cancelled. Nothing was changed.")
		return 0
	}

	log := oplog.Open("uninstall")
	defer log.Close()
	log.Write("ran", cmdline, 0)
	if err := runUninstallFunc(program, cmdline); err != nil {
		_, _ = fmt.Fprintf(stderr, "The uninstaller for %s did not finish: %v\nLeftovers were not touched.\n", app.Name, err)
		return 1
	}

	remaining := filterApps(readAppsFunc())
	for _, a := range remaining {
		if strings.EqualFold(a.Key, app.Key) {
			_, _ = fmt.Fprintf(stderr, "%s is still installed (the uninstaller was cancelled or failed). Leftovers were not touched.\n", app.Name)
			return 1
		}
	}
	_, _ = fmt.Fprintf(stdout, "%s was uninstalled.\n", app.Name)

	leftovers := keepUnprotected(findLeftovers(app, remaining, roots))
	if len(leftovers) == 0 {
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "\nLeftover folders:")
	for _, l := range leftovers {
		_, _ = fmt.Fprintf(stdout, "  %s (%s)\n", l.Path, l.Reason)
	}
	if !*yes && !confirm(stdout, in, "Move these to the Recycle Bin?") {
		_, _ = fmt.Fprintln(stdout, "Leftovers kept.")
		return 0
	}
	failed := 0
	for _, l := range leftovers {
		if err := recycleFunc(l.Path); err != nil {
			failed++
			log.Write("kept", l.Path, 0)
			_, _ = fmt.Fprintf(stdout, "  kept %s: %v\n", l.Path, err)
			continue
		}
		log.Write("trashed", l.Path, 0)
	}
	_, _ = fmt.Fprintf(stdout, "Moved %d of %d leftover folders to the Recycle Bin.\n", len(leftovers)-failed, len(leftovers))
	return 0
}

// keepUnprotected drops candidates the Windows protected-path policy refuses.
func keepUnprotected(in []Leftover) []Leftover {
	var out []Leftover
	for _, l := range in {
		if !isProtectedPath(l.Path) {
			out = append(out, l)
		}
	}
	return out
}

func confirm(w io.Writer, in *bufio.Reader, question string) bool {
	_, _ = fmt.Fprintf(w, "%s Type y and press Enter [y/N]: ", question)
	line, _ := in.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func pickApp(w io.Writer, in *bufio.Reader, apps []App) (App, bool) {
	var choices []App
	for _, a := range apps {
		if !a.Protected {
			choices = append(choices, a)
		}
	}
	if len(choices) == 0 {
		_, _ = fmt.Fprintln(w, "No removable apps found.")
		return App{}, false
	}
	for i, a := range choices {
		_, _ = fmt.Fprintf(w, "%4d  %-50s %s\n", i+1, truncate(a.Name, 50), a.Version)
	}
	_, _ = fmt.Fprint(w, "\nNumber of the app to uninstall (Enter to cancel): ")
	line, _ := in.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(choices) {
		_, _ = fmt.Fprintln(w, "Cancelled.")
		return App{}, false
	}
	return choices[n-1], true
}

func printList(w io.Writer, apps []App, asJSON bool) int {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if apps == nil {
			apps = []App{}
		}
		if err := enc.Encode(apps); err != nil {
			return 1
		}
		return 0
	}
	for _, a := range apps {
		size := ""
		if a.SizeKB > 0 {
			size = units.BytesSI(int64(a.SizeKB) * 1024)
		}
		mark := ""
		if a.Protected {
			mark = "  (protected)"
		}
		_, _ = fmt.Fprintf(w, "%-50s %-16s %10s%s\n", truncate(a.Name, 50), truncate(a.Version, 16), size, mark)
	}
	_, _ = fmt.Fprintf(w, "\n%d apps\n", len(apps))
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
