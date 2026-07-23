package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const upgradeRepoURL = "https://github.com/stefandevo/claude-dialects.git"

const upgradeManualInstructions = "upgrade manually with:\n" +
	"  git clone https://github.com/stefandevo/claude-dialects.git\n" +
	"  cd claude-dialects\n" +
	"  make install"

func upgradeCommand(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	ref := fs.String("ref", "main", "git branch or tag to build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cc-dialect upgrade [--ref <branch-or-tag>]")
	}
	return upgrade(*ref)
}

// upgrade fetches the requested ref into a temporary shallow clone, builds it
// with the same flags as the Makefile build target, atomically replaces the
// installed binary, and reconciles running proxies and bridge runtimes. Every
// step before the replacement leaves the installed binary untouched on failure.
func upgrade(ref string) error {
	gitPath, goPath, err := upgradeBuildTools(exec.LookPath)
	if err != nil {
		return err
	}
	target, err := upgradeTargetPath(os.Executable)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp("", "cc-dialect-upgrade-")
	if err != nil {
		return err
	}
	// The clone and build products are removed on every path except the ones
	// whose error message points back into the temp dir (build log, built
	// binary kept for a manual move).
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	sourceDir := filepath.Join(tempDir, "source")
	logPath := filepath.Join(tempDir, "upgrade.log")
	fmt.Printf("Fetching %s (%s)...\n", upgradeRepoURL, ref)
	if err = runUpgradeStep(logPath, "", nil, gitPath,
		"clone", "--quiet", "--depth", "1", "--branch", ref, upgradeRepoURL, sourceDir); err != nil {
		return fmt.Errorf("fetch failed, nothing was changed: %w", err)
	}

	targetVersion, err := upgradeSourceVersion(gitPath, sourceDir)
	if err != nil {
		return fmt.Errorf("could not read the fetched version, nothing was changed: %w", err)
	}
	currentVersion := CurrentAppVersion()
	if currentVersion == targetVersion {
		fmt.Printf("Already up to date (%s).\n", currentVersion)
		return nil
	}

	// Mirror the Makefile build target, but stamp the real version instead of
	// the VERSION=dev default so the next upgrade can short-circuit.
	built := filepath.Join(tempDir, "cc-dialect")
	fmt.Printf("Building cc-dialect %s...\n", targetVersion)
	buildEnv := []string{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=arm64"}
	if err = runUpgradeStep(logPath, sourceDir, buildEnv, goPath,
		"build", "-trimpath", "-ldflags=-s -w -X main.version="+targetVersion, "-o", built, "."); err != nil {
		keepTemp = true
		return fmt.Errorf("build failed, nothing was changed: %w\nthe full build log is at %s", err, logPath)
	}

	if err = replaceExecutable(target, built); err != nil {
		keepTemp = true
		return fmt.Errorf("could not replace %s: %w\nthe old binary is untouched; the new build was kept at %s — move it into place manually",
			target, err, built)
	}
	fmt.Printf("Upgraded %s: %s → %s\n", target, currentVersion, targetVersion)

	// Reconcile with the new binary, not this process: the bridge SDK pins and
	// build identity that doctor compares against are compiled in, so the old
	// process would check the old values.
	fmt.Println("Reconciling running proxies and bridge runtimes...")
	reconcile := exec.Command(target, "doctor", "--fix")
	reconcile.Stdin, reconcile.Stdout, reconcile.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = reconcile.Run(); err != nil {
		return fmt.Errorf("the upgrade completed, but reconciling runtimes failed: %w\nrun manually: cc-dialect doctor --fix", err)
	}
	return nil
}

// upgradeBuildTools verifies the tools upgrade shells out to are available
// before anything is fetched. lookPath is a parameter so tests can stub it.
func upgradeBuildTools(lookPath func(string) (string, error)) (gitPath, goPath string, err error) {
	gitPath, gitErr := lookPath("git")
	goPath, goErr := lookPath("go")
	var missing []string
	if gitErr != nil {
		missing = append(missing, "git")
	}
	if goErr != nil {
		missing = append(missing, "go (Go 1.26.5 or newer)")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("upgrade requires %s in PATH; install the missing tools and re-run cc-dialect upgrade, or %s",
			strings.Join(missing, " and "), upgradeManualInstructions)
	}
	return gitPath, goPath, nil
}

// upgradeTargetPath resolves the installed binary upgrade replaces. It refuses
// to touch a development build living inside a source checkout (for example
// dist/cc-dialect), so upgrade never clobbers a contributor's working tree.
func upgradeTargetPath(executable func() (string, error)) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}
	if root, inCheckout := devCheckoutRoot(filepath.Dir(exe)); inCheckout {
		return "", fmt.Errorf("%s is inside the source checkout %s; refusing to overwrite a development build — run `git pull && make install` there instead",
			exe, root)
	}
	return exe, nil
}

// devCheckoutRoot walks up from dir looking for this project's go.mod, which
// marks the executable as a development build rather than an installed binary.
func devCheckoutRoot(dir string) (string, bool) {
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(data), "module github.com/stefandevo/claude-dialects") {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// upgradeSourceVersion derives the version stamp for the fetched source: the
// latest tag when one exists, otherwise the short commit hash. Locally
// installed builds default to VERSION=dev, so stamping a real version here is
// what lets the next upgrade short-circuit with "already up to date".
func upgradeSourceVersion(gitPath, sourceDir string) (string, error) {
	output, err := exec.Command(gitPath, "-C", sourceDir, "describe", "--tags", "--always").Output()
	if err != nil {
		return "", fmt.Errorf("git describe: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", errors.New("git describe produced no version")
	}
	return version, nil
}

// runUpgradeStep runs one external command, appends its combined output to the
// upgrade log, and folds the output tail into the returned error so failures
// are actionable without opening the log.
func runUpgradeStep(logPath, dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	output, err := cmd.CombinedOutput()
	entry := fmt.Sprintf("$ %s %s\n%s\n", name, strings.Join(args, " "), output)
	if logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); logErr == nil {
		_, _ = logFile.WriteString(entry)
		_ = logFile.Close()
	}
	if err != nil {
		if tail := outputTail(output, 20); tail != "" {
			return fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), args[0], err, tail)
		}
		return fmt.Errorf("%s %s: %w", filepath.Base(name), args[0], err)
	}
	return nil
}

// outputTail returns up to the last lines of trimmed command output.
func outputTail(output []byte, lines int) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	all := strings.Split(trimmed, "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// replaceExecutable replaces the installed binary at target with the freshly
// built one. atomicWriteFile stages a temp file in the same directory and
// renames it into place, so a failure never leaves target truncated and
// running shims keep exec-ing a complete binary.
func replaceExecutable(target, built string) error {
	data, err := os.ReadFile(built)
	if err != nil {
		return err
	}
	return atomicWriteFile(target, data, 0o755)
}
