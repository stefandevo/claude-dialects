package app

import (
	"fmt"
	"os/exec"
	"runtime"
)

// supportedPlatform reports whether Claude Dialects is built and tested for the
// given target. The build constraints are deliberately looser than this list —
// process_unix.go carries no architecture constraint — so that an unlisted
// architecture fails with this predicate's clear message rather than a
// confusing `undefined: detach`. macOS and Linux on amd64 and arm64 are what CI
// exercises; everything else is untested.
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
// xdg-open, so every failure names the flag that skips the browser entirely,
// and Linux additionally names the package to install.
func browserCommand(goos string, lookPath func(string) (string, error)) (string, error) {
	const fallback = "re-run with --no-browser and open the printed URL yourself"
	var opener, remedy string
	switch goos {
	case "darwin":
		opener = "open"
	case "linux":
		opener, remedy = "xdg-open", "install xdg-utils, or "
	default:
		return "", fmt.Errorf("opening a browser is not supported on %s; %s", goos, fallback)
	}
	path, err := lookPath(opener)
	if err != nil {
		return "", fmt.Errorf("%s was not found in PATH; %s%s", opener, remedy, fallback)
	}
	return path, nil
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
