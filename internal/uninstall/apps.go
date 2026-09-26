// Package uninstall implements `mole uninstall` on Windows: it runs an
// application's own uninstaller, then offers leftover folders that match the
// application's exact name for the Recycle Bin. The matching rules here are
// platform-neutral so they are tested everywhere.
package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// App is one entry from the Windows Uninstall registry keys.
type App struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	Version         string `json:"version,omitempty"`
	Publisher       string `json:"publisher,omitempty"`
	InstallLocation string `json:"install_location,omitempty"`
	UninstallString string `json:"-"`
	SizeKB          uint64 `json:"size_kb,omitempty"`
	Scope           string `json:"scope"` // "machine" or "user"
	Protected       bool   `json:"protected,omitempty"`

	systemComponent bool
	parentKey       string
	releaseType     string
}

// filterApps drops entries that are not user-facing applications: system
// components, updates and hotfixes attached to a parent, and entries without
// a name or an uninstaller. Duplicates (same name and version) collapse.
func filterApps(all []App) []App {
	seen := map[string]bool{}
	var out []App
	for _, a := range all {
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || strings.TrimSpace(a.UninstallString) == "" || a.systemComponent || a.parentKey != "" {
			continue
		}
		switch strings.ToLower(a.releaseType) {
		case "update", "hotfix", "security update", "service pack":
			continue
		}
		key := strings.ToLower(a.Name + "\x00" + a.Version)
		if seen[key] {
			continue
		}
		seen[key] = true
		a.Protected = isProtectedApp(a)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// protectedAppPatterns are runtimes, drivers, OS components, and security
// agents that other software or the system depends on. They are listed but
// never uninstalled by Mole; use Windows Settings if one must go.
var protectedAppPatterns = []string{
	"microsoft visual c++", "microsoft .net", ".net runtime", ".net desktop runtime", "asp.net core",
	"microsoft edge", "webview2", "windows software development kit", "windows sdk",
	"microsoft update health", "windows pc health", "microsoft defender", "windows defender",
	"nvidia", "intel(r)", "intel ", "amd ", "realtek", "synaptics", "qualcomm", "dolby",
	"crowdstrike", "sentinelone", "sentinel agent", "eset", "palo alto", "globalprotect", "cortex xdr",
	"cisco", "sophos", "microsoft intune", "configuration manager client",
}

func isProtectedApp(a App) bool {
	name := strings.ToLower(a.Name)
	for _, p := range protectedAppPatterns {
		if strings.HasPrefix(name, p) || strings.Contains(name, " "+p) {
			return true
		}
	}
	pub := strings.ToLower(a.Publisher)
	for _, p := range []string{"nvidia", "intel corporation", "advanced micro devices", "realtek", "crowdstrike", "sentinelone", "eset", "palo alto networks", "sophos"} {
		if strings.HasPrefix(pub, p) {
			return true
		}
	}
	return false
}

var msiGUID = regexp.MustCompile(`(?i)\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}`)

// uninstallCommand turns an UninstallString into the program and raw command
// line to run. MsiExec entries always become "msiexec.exe /x {GUID}"; many
// installers register /I (repair UI) instead of /X.
func uninstallCommand(uninstallString, systemRoot string) (program, cmdline string, err error) {
	s := strings.TrimSpace(uninstallString)
	if s == "" {
		return "", "", fmt.Errorf("no uninstaller registered")
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimLeft(s, `"`)), "msiexec") {
		guid := msiGUID.FindString(s)
		if guid == "" {
			return "", "", fmt.Errorf("MsiExec uninstaller without a product code: %s", s)
		}
		program = filepath.Join(systemRoot, "System32", "msiexec.exe")
		return program, `"` + program + `" /x ` + strings.ToUpper(guid), nil
	}
	// Quoted executable: "C:\Program Files\App\unins000.exe" /args
	if strings.HasPrefix(s, `"`) {
		end := strings.Index(s[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("unbalanced quotes in uninstaller: %s", s)
		}
		return s[1 : end+1], s, nil
	}
	// Unquoted: take the longest prefix ending in .exe.
	lower := strings.ToLower(s)
	if i := strings.Index(lower, ".exe"); i >= 0 {
		program = s[:i+4]
		return program, `"` + program + `"` + s[i+4:], nil
	}
	return "", "", fmt.Errorf("cannot find the uninstaller program in: %s", s)
}

// genericNames are words that name too many things to identify one app.
var genericNames = map[string]bool{
	"app": true, "apps": true, "application": true, "data": true, "cache": true, "caches": true,
	"common": true, "common files": true, "update": true, "updater": true, "updates": true,
	"tools": true, "tool": true, "software": true, "program": true, "programs": true,
	"user": true, "users": true, "config": true, "settings": true, "local": true, "roaming": true,
	"temp": true, "microsoft": true, "windows": true, "google": true, "packages": true,
	"logs": true, "log": true, "crashpad": true, "plugins": true, "setup": true, "installer": true,
	"launcher": true, "client": true, "service": true, "helper": true, "driver": true, "drivers": true,
	"system": true, "shared": true, "runtime": true, "default": true, "desktop": true, "documents": true,
}

const minVariantLength = 4

var versionSuffix = regexp.MustCompile(`(?i)(\s+v?\d+(\.\d+)*[a-z]?)?(\s*\((x64|x86|64-bit|32-bit|arm64)\))?\s*$`)

// nameVariants returns the exact folder names that identify app: its display
// name, the name without a trailing version or architecture, and the folder
// of its install location. The publisher is never a variant on its own.
func nameVariants(a App) []string {
	stripped := strings.TrimSpace(versionSuffix.ReplaceAllString(a.Name, ""))
	// An app whose name, without its version, is generic or very short has no
	// identity strong enough to match folders by, so it gets no variants.
	if len(stripped) < minVariantLength || genericNames[strings.ToLower(stripped)] {
		return nil
	}
	candidates := []string{a.Name, stripped}
	if loc := strings.TrimRight(strings.TrimSpace(a.InstallLocation), `\/`); loc != "" {
		candidates = append(candidates, baseName(loc))
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		key := strings.ToLower(c)
		if len(c) < minVariantLength || genericNames[key] || seen[key] || strings.ContainsAny(c, `\/:*?"<>|`) {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// baseName is filepath.Base for Windows paths on any platform.
func baseName(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Leftover is one folder offered for the Recycle Bin.
type Leftover struct {
	Path   string
	Reason string
}

// leftoverRoots are the per-user and machine data roots searched for folders
// named exactly like the app, directly or one level under its publisher.
type leftoverRoots struct {
	AppData, LocalAppData, ProgramData string
}

// findLeftovers lists folders whose name equals one of app's variants.
// Folders that a still-installed app also claims (same variant, or its
// install location inside or around the folder) are kept.
func findLeftovers(app App, stillInstalled []App, roots leftoverRoots) []Leftover {
	variants := nameVariants(app)
	if len(variants) == 0 {
		return nil
	}
	var bases []string
	for _, r := range []string{roots.AppData, roots.LocalAppData, roots.ProgramData} {
		if r == "" {
			continue
		}
		bases = append(bases, r)
		if pub := strings.TrimSpace(app.Publisher); len(pub) >= minVariantLength && !genericNames[strings.ToLower(pub)] && !strings.ContainsAny(pub, `\/:*?"<>|`) {
			bases = append(bases, filepath.Join(r, pub))
		}
	}
	if roots.LocalAppData != "" {
		bases = append(bases, filepath.Join(roots.LocalAppData, "Programs"))
	}

	claimed := map[string]bool{}
	for _, other := range stillInstalled {
		if strings.EqualFold(other.Key, app.Key) {
			continue
		}
		for _, v := range nameVariants(other) {
			claimed[strings.ToLower(v)] = true
		}
	}

	seen := map[string]bool{}
	var out []Leftover
	consider := func(path, reason string) {
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			return
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 {
			return
		}
		if claimed[strings.ToLower(baseName(path))] {
			return
		}
		for _, other := range stillInstalled {
			if strings.EqualFold(other.Key, app.Key) || other.InstallLocation == "" {
				continue
			}
			if pathWithin(other.InstallLocation, path) || pathWithin(path, other.InstallLocation) {
				return
			}
		}
		seen[key] = true
		out = append(out, Leftover{Path: path, Reason: reason})
	}

	for _, base := range bases {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			for _, v := range variants {
				if strings.EqualFold(e.Name(), v) {
					consider(filepath.Join(base, e.Name()), "named "+v)
				}
			}
		}
	}
	// The registered install location counts only when its folder name is the
	// app's own name: installers sometimes register a shared parent such as
	// D:\Games, which must never be offered as a whole.
	if loc := strings.TrimSpace(app.InstallLocation); loc != "" {
		folder := strings.ToLower(baseName(loc))
		for _, n := range []string{app.Name, strings.TrimSpace(versionSuffix.ReplaceAllString(app.Name, ""))} {
			if len(n) >= minVariantLength && folder == strings.ToLower(strings.TrimSpace(n)) {
				consider(filepath.Clean(loc), "install location")
				break
			}
		}
	}
	return out
}

// pathWithin reports whether path is root or below it, case-insensitively.
func pathWithin(path, root string) bool {
	path = strings.ToLower(filepath.Clean(path))
	root = strings.ToLower(filepath.Clean(root))
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(path, strings.TrimSuffix(root, sep)+sep)
}

// matchApps returns apps whose name contains query, case-insensitively; an
// exact name match wins on its own.
func matchApps(apps []App, query string) []App {
	q := strings.ToLower(strings.TrimSpace(query))
	var exact, partial []App
	for _, a := range apps {
		name := strings.ToLower(a.Name)
		switch {
		case name == q:
			exact = append(exact, a)
		case strings.Contains(name, q):
			partial = append(partial, a)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}
