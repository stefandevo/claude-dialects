package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cc-dialect")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildIdentityUsesExplicitReleaseVersion(t *testing.T) {
	exe := writeTempBinary(t, "binary-a")
	identity := buildIdentity("v1.2.3", func() (string, error) { return exe, nil })
	if identity != "v1.2.3" {
		t.Fatalf("expected release version to be used verbatim, got %q", identity)
	}
}

func TestBuildIdentityForDevBuildsHashesExecutable(t *testing.T) {
	exe := writeTempBinary(t, "binary-a")
	executable := func() (string, error) { return exe, nil }

	identity := buildIdentity("dev", executable)
	if !strings.HasPrefix(identity, "dev-") {
		t.Fatalf("expected dev identity to carry a dev- prefix, got %q", identity)
	}
	if identity == "dev" {
		t.Fatal("dev builds must not stamp the bare version string; a reinstall would be undetectable")
	}
	if again := buildIdentity("dev", executable); again != identity {
		t.Fatalf("identity must be deterministic for the same binary: %q vs %q", identity, again)
	}
}

func TestBuildIdentityChangesWhenBinaryChanges(t *testing.T) {
	oldExe := writeTempBinary(t, "binary-old")
	newExe := writeTempBinary(t, "binary-new")

	oldIdentity := buildIdentity("dev", func() (string, error) { return oldExe, nil })
	newIdentity := buildIdentity("dev", func() (string, error) { return newExe, nil })
	if oldIdentity == newIdentity {
		t.Fatalf("different binaries must produce different identities, both %q", oldIdentity)
	}
}

func TestBuildIdentityFallsBackWhenExecutableUnreadable(t *testing.T) {
	identity := buildIdentity("dev", func() (string, error) { return "", errors.New("no executable") })
	if identity != "dev" {
		t.Fatalf("expected bare dev fallback when the binary cannot be hashed, got %q", identity)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	identity = buildIdentity("", func() (string, error) { return missing, nil })
	if identity != "dev" {
		t.Fatalf("expected bare dev fallback for missing file, got %q", identity)
	}
}

func TestBuildIdentityTreatsEmptyVersionAsDev(t *testing.T) {
	exe := writeTempBinary(t, "binary-a")
	identity := buildIdentity("", func() (string, error) { return exe, nil })
	if !strings.HasPrefix(identity, "dev-") {
		t.Fatalf("empty version must hash the executable like dev, got %q", identity)
	}
}
