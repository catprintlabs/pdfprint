package gs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

// FindBinary resolves the Ghostscript executable to use.
//
// It tries, in order:
//  1. the preferred name/path, if it exists on PATH or on disk (the --gs override);
//  2. a Ghostscript bundled next to this executable — so an app that ships gs as
//     a sibling (e.g. an Electron app's extraResources) gets a pinned, known-good
//     version regardless of what's installed system-wide;
//  3. on Windows, the standard installer locations
//     (C:\Program Files\gs\gs*\bin\gswin64c.exe and 32-bit / x86 variants),
//     newest version first — because the Ghostscript installer frequently does
//     not add itself to PATH; on macOS/Linux, gs on PATH;
//  4. the bare conventional name as a last resort, so exec produces a clear
//     "not found" error the caller can report.
//
// Returns the resolved path and whether it was actually found.
func FindBinary(preferred string) (string, bool) {
	if preferred != "" {
		if p, err := exec.LookPath(preferred); err == nil {
			return p, true
		}
	}

	// A bundled gs beside our own binary wins over the system one (reproducible).
	if p := findSiblingGS(); p != "" {
		return p, true
	}

	if runtime.GOOS == "windows" {
		if p := findWindowsGS(); p != "" {
			return p, true
		}
	} else {
		// macOS/Linux: gs is normally on PATH.
		if p, err := exec.LookPath("gs"); err == nil {
			return p, true
		}
	}

	if preferred != "" {
		return preferred, false
	}
	if runtime.GOOS == "windows" {
		return "gswin64c.exe", false
	}
	return "gs", false
}

// findSiblingGS looks for a Ghostscript binary bundled alongside this executable
// (or one level down in gs/ or gs/bin/). This is how a packaged app ships a
// private, version-pinned gs without touching the system.
func findSiblingGS() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)

	var candidates []string
	if runtime.GOOS == "windows" {
		for _, name := range []string{"gswin64c.exe", "gswin32c.exe"} {
			candidates = append(candidates,
				filepath.Join(dir, name),
				filepath.Join(dir, "gs", name),
				filepath.Join(dir, "gs", "bin", name),
			)
		}
	} else {
		candidates = []string{
			filepath.Join(dir, "gs"),
			filepath.Join(dir, "gs", "bin", "gs"),
		}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// findWindowsGS globs the standard Ghostscript install trees and returns the
// newest 64-bit console binary it can find (falling back to 32-bit).
func findWindowsGS() string {
	patterns := []string{
		`C:\Program Files\gs\gs*\bin\gswin64c.exe`,
		`C:\Program Files (x86)\gs\gs*\bin\gswin64c.exe`,
		`C:\Program Files\gs\gs*\bin\gswin32c.exe`,
		`C:\Program Files (x86)\gs\gs*\bin\gswin32c.exe`,
	}
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil || len(matches) == 0 {
			continue
		}
		// Newest version last alphabetically (gs10.02 > gs10.01); pick highest.
		sort.Strings(matches)
		return matches[len(matches)-1]
	}
	return ""
}
