package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilotRuntimePackageJSONPinsSDKAndCLI(t *testing.T) {
	raw := copilotRuntimePackageJSON()
	if strings.Contains(string(raw), "^") || strings.Contains(string(raw), "~") {
		t.Fatalf("runtime package.json must pin exact versions, got:\n%s", raw)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if got, want := manifest.Dependencies["@github/copilot-sdk"], copilotSDKVersion; got != want {
		t.Fatalf("@github/copilot-sdk = %q, want %q", got, want)
	}
	if got, want := manifest.Dependencies["@github/copilot"], copilotCLIVersion; got != want {
		t.Fatalf("@github/copilot = %q, want %q", got, want)
	}
	if copilotSDKVersion != "1.0.22" || copilotCLIVersion != "1.0.98" {
		t.Fatalf("pins moved to sdk %s cli %s; update the docs and this assertion together", copilotSDKVersion, copilotCLIVersion)
	}
}

func TestCopilotStatusReportsMissingCLIWhenOnlySDKIsInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeCopilotPackage(t, home, "@github/copilot-sdk", copilotSDKVersion)

	ready, line := copilotCLIStatusLine()
	if ready || !strings.Contains(line, "@github/copilot CLI is not installed") {
		t.Fatalf("cli status ready=%v line=%q", ready, line)
	}
	sdkReady, sdkLine := copilotSDKStatusLine()
	if !sdkReady || !strings.Contains(sdkLine, copilotSDKVersion) {
		t.Fatalf("sdk status ready=%v line=%q", sdkReady, sdkLine)
	}
	if err := requireCopilotRuntime(); err == nil || !strings.Contains(err.Error(), "Copilot CLI is not installed") {
		t.Fatalf("requireCopilotRuntime() = %v", err)
	}
}

func TestCopilotStatusReportsWrongCLIVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeCopilotPackage(t, home, "@github/copilot-sdk", copilotSDKVersion)
	writeCopilotPackage(t, home, "@github/copilot", "1.0.80")
	writeCopilotLoginBin(t, home)

	ready, line := copilotCLIStatusLine()
	if ready || !strings.Contains(line, "1.0.80") || !strings.Contains(line, copilotCLIVersion) {
		t.Fatalf("cli status ready=%v line=%q", ready, line)
	}
}

func TestCopilotStatusReportsCLIWithoutLoginExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeCopilotPackage(t, home, "@github/copilot-sdk", copilotSDKVersion)
	writeCopilotPackage(t, home, "@github/copilot", copilotCLIVersion)

	ready, line := copilotCLIStatusLine()
	if ready || !strings.Contains(line, "login executable") {
		t.Fatalf("cli status ready=%v line=%q", ready, line)
	}
	if err := requireCopilotRuntime(); err == nil || !strings.Contains(err.Error(), "login executable") {
		t.Fatalf("requireCopilotRuntime() = %v", err)
	}
}

func TestCopilotStatusAcceptsPinnedCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeCopilotPackage(t, home, "@github/copilot-sdk", copilotSDKVersion)
	writeCopilotPackage(t, home, "@github/copilot", copilotCLIVersion)
	writeCopilotLoginBin(t, home)

	sdkReady, sdkLine := copilotSDKStatusLine()
	cliReady, cliLine := copilotCLIStatusLine()
	if !sdkReady || !cliReady {
		t.Fatalf("sdk %v %q cli %v %q", sdkReady, sdkLine, cliReady, cliLine)
	}
	sdk, cli, err := requireInstalledCopilotRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if sdk != copilotSDKVersion || cli != copilotCLIVersion {
		t.Fatalf("installed sdk %s cli %s", sdk, cli)
	}
}

func TestCopilotStatusRejectsNonExecutableLoginBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeCopilotPackage(t, home, "@github/copilot-sdk", copilotSDKVersion)
	writeCopilotPackage(t, home, "@github/copilot", copilotCLIVersion)
	bin := filepath.Join(home, "copilot-runtime", "node_modules", ".bin", "copilot")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ready, line := copilotCLIStatusLine()
	if ready || !strings.Contains(line, "not executable") {
		t.Fatalf("cli status ready=%v line=%q", ready, line)
	}
}

func writeCopilotPackage(t *testing.T, home, name, version string) {
	t.Helper()
	parts := strings.Split(name, "/")
	dir := filepath.Join(append([]string{home, "copilot-runtime", "node_modules"}, parts...)...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"name":"` + name + `","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCopilotLoginBin(t *testing.T, home string) {
	t.Helper()
	bin := filepath.Join(home, "copilot-runtime", "node_modules", ".bin", "copilot")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
