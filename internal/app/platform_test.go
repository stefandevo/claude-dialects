package app

import (
	"errors"
	"strings"
	"testing"
)

func TestSupportedPlatform(t *testing.T) {
	supported := []struct{ goos, goarch string }{
		{"darwin", "arm64"},
		{"darwin", "amd64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
	}
	for _, target := range supported {
		if !supportedPlatform(target.goos, target.goarch) {
			t.Fatalf("expected %s/%s to be supported", target.goos, target.goarch)
		}
	}

	unsupported := []struct{ goos, goarch string }{
		{"windows", "amd64"},
		{"freebsd", "amd64"},
		{"linux", "386"},
		{"darwin", "riscv64"},
	}
	for _, target := range unsupported {
		if supportedPlatform(target.goos, target.goarch) {
			t.Fatalf("expected %s/%s to be unsupported", target.goos, target.goarch)
		}
	}
}

func TestBrowserCommandPerPlatform(t *testing.T) {
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	path, err := browserCommand("darwin", found)
	if err != nil {
		t.Fatalf("darwin browser command: %v", err)
	}
	if path != "/usr/bin/open" {
		t.Fatalf("expected darwin to use open, got %q", path)
	}

	path, err = browserCommand("linux", found)
	if err != nil {
		t.Fatalf("linux browser command: %v", err)
	}
	if path != "/usr/bin/xdg-open" {
		t.Fatalf("expected linux to use xdg-open, got %q", path)
	}
}

func TestBrowserCommandMissingOpenerPointsAtNoBrowser(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }

	_, err := browserCommand("linux", missing)
	if err == nil {
		t.Fatal("expected an error when xdg-open is missing")
	}
	if !strings.Contains(err.Error(), "--no-browser") || !strings.Contains(err.Error(), "xdg-utils") {
		t.Fatalf("error should name xdg-utils and --no-browser, got %q", err)
	}
}

func TestBrowserCommandUnsupportedPlatform(t *testing.T) {
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	_, err := browserCommand("windows", found)
	if err == nil {
		t.Fatal("expected an error on an unsupported platform")
	}
	if !strings.Contains(err.Error(), "--no-browser") {
		t.Fatalf("error should point at --no-browser, got %q", err)
	}
}
