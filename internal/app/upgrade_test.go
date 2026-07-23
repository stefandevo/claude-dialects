package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCommandRejectsExtraArguments(t *testing.T) {
	err := upgradeCommand([]string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "usage: cc-dialect upgrade") {
		t.Fatalf("expected usage error for extra arguments, got %v", err)
	}
}

func TestUpgradeBuildToolsMissing(t *testing.T) {
	missing := func(tools ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, tool := range tools {
				if name == tool {
					return "", errors.New("not found")
				}
			}
			return "/usr/bin/" + name, nil
		}
	}
	_, _, err := upgradeBuildTools(missing("git"))
	if err == nil || !strings.Contains(err.Error(), "git") || !strings.Contains(err.Error(), "make install") {
		t.Fatalf("expected missing git error with manual instructions, got %v", err)
	}
	_, _, err = upgradeBuildTools(missing("go"))
	if err == nil || !strings.Contains(err.Error(), "go (Go") {
		t.Fatalf("expected missing go error, got %v", err)
	}
	_, _, err = upgradeBuildTools(missing("git", "go"))
	if err == nil || !strings.Contains(err.Error(), "git and go") {
		t.Fatalf("expected both tools reported, got %v", err)
	}
}

func TestUpgradeBuildToolsFound(t *testing.T) {
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	gitPath, goPath, err := upgradeBuildTools(lookPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gitPath != "/usr/bin/git" || goPath != "/usr/bin/go" {
		t.Fatalf("unexpected tool paths %q, %q", gitPath, goPath)
	}
}

func TestUpgradeTargetPathRefusesDevCheckout(t *testing.T) {
	checkout := t.TempDir()
	gomod := "module github.com/stefandevo/claude-dialects\n\ngo 1.26.5\n"
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	distDir := filepath.Join(checkout, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(distDir, "cc-dialect")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := upgradeTargetPath(func() (string, error) { return exe, nil })
	if err == nil || !strings.Contains(err.Error(), "make install") {
		t.Fatalf("expected dev checkout refusal pointing at make install, got %v", err)
	}
}

func TestUpgradeTargetPathIgnoresOtherCheckouts(t *testing.T) {
	checkout := t.TempDir()
	gomod := "module github.com/example/other-project\n\ngo 1.26.5\n"
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(checkout, "cc-dialect")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := upgradeTargetPath(func() (string, error) { return exe, nil })
	if err != nil {
		t.Fatalf("expected no error for unrelated checkout, got %v", err)
	}
	expected, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestUpgradeTargetPathResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "cc-dialect")
	if err := os.WriteFile(real, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cc-dialect-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := upgradeTargetPath(func() (string, error) { return link, nil })
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got != expected {
		t.Fatalf("expected symlink resolved to %q, got %q", expected, got)
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cc-dialect")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(dir, "built")
	if err := os.WriteFile(built, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, built); err != nil {
		t.Fatalf("expected replacement to succeed, got %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("expected target to hold the new binary, got %q", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected mode 755, got %v", info.Mode().Perm())
	}
}

func TestReplaceExecutableMissingBuild(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cc-dialect")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing build output")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("expected target untouched after failure, got %q", data)
	}
}

func TestOutputTail(t *testing.T) {
	if got := outputTail(nil, 3); got != "" {
		t.Fatalf("expected empty tail for no output, got %q", got)
	}
	if got := outputTail([]byte("one\ntwo\n"), 3); got != "one\ntwo" {
		t.Fatalf("expected full trimmed output, got %q", got)
	}
	if got := outputTail([]byte("one\ntwo\nthree\nfour\n"), 2); got != "three\nfour" {
		t.Fatalf("expected last two lines, got %q", got)
	}
}

func TestUpgradeSourceVersion(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitPath, append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("git %v: %v\n%s", args, runErr, output)
		}
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("commit", "--quiet", "-m", "initial")

	// Without tags the version falls back to the short commit hash.
	version, err := upgradeSourceVersion(gitPath, dir)
	if err != nil {
		t.Fatalf("expected version from untagged repository, got %v", err)
	}
	if version == "" || version == "dev" {
		t.Fatalf("expected a commit-derived version, got %q", version)
	}

	run("tag", "v1.2.3")
	version, err = upgradeSourceVersion(gitPath, dir)
	if err != nil {
		t.Fatalf("expected version from tagged repository, got %v", err)
	}
	if version != "v1.2.3" {
		t.Fatalf("expected tag version v1.2.3, got %q", version)
	}
}
