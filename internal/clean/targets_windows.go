//go:build windows

package clean

import (
	"os"
	"path/filepath"
	"time"
)

// target is one directory whose contents are rebuildable. Its root is never
// deleted, only files below it (and directories that end up empty).
type target struct {
	section string
	name    string
	root    string
	// minAge keeps files modified more recently than this; zero keeps none.
	minAge time.Duration
	// guard names process images (lowercase, e.g. "chrome.exe") that own the
	// target. The target is skipped while any runs or when that is unknown.
	guard []string
	// owner is the application name used in skip messages.
	owner string
}

// Deliberately NOT targets, because they hold authored, installed, or session
// state, or belong to Windows Update: ~\.nuget\packages, ~\.m2, ~\.gradle,
// ~\.cargo\registry\src, browser profiles (cookies, Local Storage, sessions),
// Explorer thumbcache (tiny UI state), anything under %SystemRoot% including
// SoftwareDistribution, $Recycle.Bin, and OneDrive.

// cleanTargets lists the user-scope targets that exist on this machine.
func cleanTargets() []target {
	local := os.Getenv("LOCALAPPDATA")
	var targets []target
	add := func(t target) {
		if t.root == "" {
			return
		}
		t.root = filepath.Clean(t.root)
		for _, existing := range targets {
			if pathEqualFold(existing.root, t.root) {
				return
			}
		}
		targets = append(targets, t)
	}

	const day = 24 * time.Hour
	add(target{section: "System temp", name: "Temp files older than 1 day", root: os.Getenv("TEMP"), minAge: day})
	if local != "" {
		add(target{section: "System temp", name: "Temp files older than 1 day", root: filepath.Join(local, "Temp"), minAge: day})
	}

	if local != "" {
		win := filepath.Join(local, "Microsoft", "Windows")
		add(target{section: "Windows caches", name: "Internet cache", root: filepath.Join(win, "INetCache")})
		add(target{section: "Windows caches", name: "Crash dumps", root: filepath.Join(local, "CrashDumps")})
		add(target{section: "Windows caches", name: "Error report archive", root: filepath.Join(win, "WER", "ReportArchive")})
		add(target{section: "Windows caches", name: "Error report queue", root: filepath.Join(win, "WER", "ReportQueue")})

		for _, b := range []struct {
			owner, userData, image string
		}{
			{"Chrome", filepath.Join(local, "Google", "Chrome", "User Data"), "chrome.exe"},
			{"Edge", filepath.Join(local, "Microsoft", "Edge", "User Data"), "msedge.exe"},
			{"Brave", filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data"), "brave.exe"},
		} {
			for _, cacheName := range []string{"Cache", "Code Cache", "GPUCache"} {
				matches, _ := filepath.Glob(filepath.Join(b.userData, "*", cacheName))
				for _, m := range matches {
					profile := filepath.Base(filepath.Dir(m))
					add(target{section: "Browsers", name: b.owner + " " + cacheName + " (" + profile + ")", root: m, guard: []string{b.image}, owner: b.owner})
				}
			}
		}
		firefox, _ := filepath.Glob(filepath.Join(local, "Mozilla", "Firefox", "Profiles", "*", "cache2"))
		for _, m := range firefox {
			add(target{section: "Browsers", name: "Firefox cache (" + filepath.Base(filepath.Dir(m)) + ")", root: m, guard: []string{"firefox.exe"}, owner: "Firefox"})
		}

		add(target{section: "Developer", name: "npm cache", root: filepath.Join(local, "npm-cache", "_cacache"), guard: []string{"node.exe"}, owner: "Node.js"})
		add(target{section: "Developer", name: "pip cache", root: filepath.Join(local, "pip", "Cache"), guard: []string{"pip.exe", "pip3.exe"}, owner: "pip"})
		add(target{section: "Developer", name: "Yarn cache", root: filepath.Join(local, "Yarn", "Cache"), guard: []string{"node.exe"}, owner: "Node.js"})
		add(target{section: "Developer", name: "uv cache", root: filepath.Join(local, "uv", "cache"), guard: []string{"uv.exe"}, owner: "uv"})
		add(target{section: "Developer", name: "Go build cache", root: filepath.Join(local, "go-build"), guard: []string{"go.exe"}, owner: "Go"})
		add(target{section: "Developer", name: "NuGet HTTP cache", root: filepath.Join(local, "NuGet", "v3-cache")})
	}
	return targets
}
