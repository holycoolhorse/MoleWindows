// Command mole is the single-binary entry point used on Windows. Like the
// `mole` shell script on macOS it is a router only: it picks the subcommand and
// hands the remaining arguments to that command's package.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tw93/mole/internal/status"
)

// version is stamped at build time with -ldflags "-X main.version=<ver>".
var version = "dev"

// unsupportedCommands are macOS-only features of the shell CLI. Naming them
// here lets the router answer clearly instead of reporting an unknown command.
var unsupportedCommands = map[string]bool{
	"clean":      true,
	"uninstall":  true,
	"optimize":   true,
	"purge":      true,
	"installer":  true,
	"history":    true,
	"touchid":    true,
	"completion": true,
	"update":     true,
	"remove":     true,
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run dispatches one invocation and returns the exit code. The subcommands
// read os.Args and may call os.Exit themselves, so they are started last.
func run(argv []string, stdout, stderr io.Writer) int {
	// Always "mole": release assets are named mole-windows-<arch>.exe, and the
	// subcommands' own help already says "mole".
	const prog = "mole"
	if len(argv) < 2 {
		printUsage(stdout, prog)
		return 0
	}

	cmd := strings.ToLower(argv[1])
	rest := argv[2:]
	switch cmd {
	case "analyze", "analyse":
		os.Args = append([]string{prog + " analyze"}, rest...)
		return runAnalyze()
	case "clean":
		if cleanSupported {
			return runClean(rest)
		}
	case "status":
		os.Args = append([]string{prog + " status"}, rest...)
		status.Main()
		return 0
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(stdout, "Mole version %s\n", version)
		return 0
	case "help", "--help", "-h":
		printUsage(stdout, prog)
		return 0
	}

	if unsupportedCommands[cmd] {
		_, _ = fmt.Fprintf(stderr, "%s %s is not available on this platform yet.\n", prog, cmd)
		_, _ = fmt.Fprintf(stderr, "Available commands: analyze, clean, status. Run '%s help' for details.\n", prog)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "Unknown command: %s\n", argv[1])
	_, _ = fmt.Fprintf(stderr, "Run '%s help' for usage information\n", prog)
	return 1
}

func printUsage(w io.Writer, prog string) {
	_, _ = fmt.Fprintf(w, `Mole %s - disk explorer and system health for the terminal

Usage: %s <command> [options]

Commands:
  analyze [PATH]   Explore disk usage; move selections to the Recycle Bin
  clean            Remove rebuildable caches and old temp files (--dry-run)
  status           Live system health dashboard (--json, --watch)
  version          Show the installed version
  help             Show this help message

Run '%s <command> --help' for command options.
`, version, prog, prog)
}
