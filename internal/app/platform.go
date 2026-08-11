package app

import (
	"fmt"
	"os/exec"
	"runtime"
)

// supportedPlatform reports whether Claude Dialects is built and tested for the
// given target. The build constraints are deliberately wider than this list —
// process_unix.go compiles on any unix — so that an unlisted platform fails with
// this predicate's clear message rather than a confusing linker error. macOS and
// Linux on amd64 and arm64 are what CI exercises; everything else is untested.
func supportedPlatform(goos, goarch string) bool {
	switch goos {
	case "darwin", "linux":
	default:
		return false
	}
	switch goarch {
	case "amd64", "arm64":
		return true
	default:
		return false
	}
}

// browserCommand resolves the helper that opens a URL in the host's default
// browser. lookPath is a parameter so tests can stub PATH lookups. Headless
// Linux (SSH sessions, containers, minimal images) frequently ships without
// xdg-open, so the error names the package to install and the flag that skips
// the browser entirely.
func browserCommand(goos string, lookPath func(string) (string, error)) (string, error) {
	switch goos {
	case "darwin":
		path, err := lookPath("open")
		if err != nil {
			return "", fmt.Errorf("open was not found in PATH: %w", err)
		}
		return path, nil
	case "linux":
		path, err := lookPath("xdg-open")
		if err != nil {
			return "", fmt.Errorf("xdg-open was not found in PATH; install xdg-utils, or re-run with --no-browser and open the printed URL yourself")
		}
		return path, nil
	default:
		return "", fmt.Errorf("opening a browser is not supported on %s; re-run with --no-browser and open the printed URL yourself", goos)
	}
}

// openBrowser launches the host's default browser for rawURL without waiting
// for it to exit.
func openBrowser(rawURL string) error {
	opener, err := browserCommand(runtime.GOOS, exec.LookPath)
	if err != nil {
		return err
	}
	return exec.Command(opener, rawURL).Start()
}
