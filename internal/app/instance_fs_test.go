package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestOpenInstanceFSRejectsSymlinkedInstancesAnchor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, "instances")); err != nil {
		t.Fatal(err)
	}

	instance, err := openInstanceFS("cc-test")
	if err == nil {
		_ = instance.Close()
		t.Fatal("openInstanceFS should reject a symlinked instances directory")
	}
	if entries, readErr := os.ReadDir(target); readErr != nil || len(entries) != 0 {
		t.Fatalf("symlink target was modified: entries=%v err=%v", entries, readErr)
	}
}

// A symlink pointing at a sibling dialect stays inside the shared instances
// root, so os.Root confinement to that root would follow it. Only the
// per-dialect root refuses it, which is what keeps one dialect's credentials,
// config and history unreachable from another.
func TestInstanceFSRejectsSiblingDialectSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	victim := filepath.Join(home, "instances", "cc-victim", "auth")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(home, "instances", "cc-attacker")
	if err := os.MkdirAll(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "cc-victim", "auth"), filepath.Join(attacker, "auth")); err != nil {
		t.Fatal(err)
	}

	instance, err := openInstanceFS("cc-attacker")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, readErr := instance.ReadDir("auth"); readErr == nil {
		t.Fatal("listed a sibling dialect's auth directory")
	}
	if _, readErr := instance.ReadFile(filepath.Join("auth", "codex.json")); readErr == nil {
		t.Fatal("read a sibling dialect's credentials")
	}
	if writeErr := instance.AtomicWrite(filepath.Join("auth", "planted.json"), []byte("x"), 0o600); writeErr == nil {
		t.Fatal("wrote into a sibling dialect's auth directory")
	}
	entries, err := os.ReadDir(victim)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "codex.json" {
		t.Fatalf("sibling dialect directory was modified: %v", entries)
	}
}

// hasProviderCredentials is the read path that made the sibling-symlink gap
// exploitable: it lists auth/ and parses every entry it finds there.
func TestHasProviderCredentialsRejectsSiblingDialectSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	victim := filepath.Join(home, "instances", "cc-victim", "auth")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(home, "instances", "cc-attacker")
	if err := os.MkdirAll(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "cc-victim", "auth"), filepath.Join(attacker, "auth")); err != nil {
		t.Fatal(err)
	}
	if hasProviderCredentials("cc-attacker", "codex") {
		t.Fatal("one dialect reported another dialect's credentials as its own")
	}
}

func TestOpenInstanceFSAllowsSymlinkedDialectHome(t *testing.T) {
	realHome := t.TempDir()
	link := filepath.Join(t.TempDir(), "dialect-home")
	if err := os.Symlink(realHome, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIALECT_HOME", link)

	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if err = instance.AtomicWrite("probe", []byte("rooted"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(realHome, "instances", "cc-test", "probe"))
	if err != nil || string(data) != "rooted" {
		t.Fatalf("rooted write = %q, %v", data, err)
	}
}

// seedStatusline is the write-side counterpart: a claude/ link pointing at a
// sibling would land this dialect's settings.json in the other dialect.
func TestSeedStatuslineRejectsSiblingDialectSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	victim := filepath.Join(home, "instances", "cc-victim", "claude")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(home, "instances", "cc-attacker")
	if err := os.MkdirAll(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "cc-victim", "claude"), filepath.Join(attacker, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-attacker", presets["codex"]); err == nil {
		t.Fatal("seedStatusline should reject a claude directory linked at a sibling dialect")
	}
	entries, err := os.ReadDir(victim)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("seeding wrote into a sibling dialect: %v", entries)
	}
}

// An entry appearing after a directory scan must not orphan the instance: the
// removal has to rescan rather than fail its single Remove with ENOTEMPTY.
func TestRemoveAllAtRescansAfterConcurrentWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := instancesRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	original := readDirAt
	raced := false
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if rel == "cc-test" && !raced {
			raced = true
			// A writer slips an entry in after the scan but before the Remove.
			if writeErr := os.WriteFile(filepath.Join(dir, "late"), []byte("y"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	if err = removeAllAt(root, "cc-test"); err != nil {
		t.Fatalf("removeAllAt did not recover from a concurrent write: %v", err)
	}
	if !raced {
		t.Fatal("test did not exercise the race")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("instance directory survived removal: %v", statErr)
	}
}

// A directory another process keeps refilling must surface the Remove error
// rather than retry forever.
func TestRemoveAllAtGivesUpOnUnendingWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := instancesRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	original := readDirAt
	scans := 0
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if rel == "cc-test" {
			scans++
			name := filepath.Join(dir, "late-"+strconv.Itoa(scans))
			if writeErr := os.WriteFile(name, []byte("y"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	if err = removeAllAt(root, "cc-test"); err == nil {
		t.Fatal("removeAllAt should report the failed removal of a directory being refilled")
	}
	if scans > maxRemoveAllRescans+1 {
		t.Fatalf("removeAllAt rescanned %d times, want at most %d", scans, maxRemoveAllRescans+1)
	}
}

func TestInstanceFSAbsRejectsNonLocalPaths(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	for _, path := range []string{"../escape", "/absolute", "a/../../escape"} {
		t.Run(path, func(t *testing.T) {
			if _, pathErr := instance.Abs(path); pathErr == nil {
				t.Fatalf("Abs(%q) should reject a non-local path", path)
			}
		})
	}
	got, err := instance.Abs(filepath.Join("auth", "token.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(os.Getenv("DIALECT_HOME"), "instances", "cc-test", "auth", "token.json")
	if got != want {
		t.Fatalf("Abs() = %q, want %q", got, want)
	}
}

func TestInstanceFSAtomicWriteSuccessAndPreCommitCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	if err = instance.AtomicWrite("state.json", []byte("rooted"), 0o640); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "instances", "cc-test", "state.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("atomic write mode = %o, want 640", info.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.tmp-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("atomic write leftovers = %v, %v", leftovers, err)
	}

	target := t.TempDir()
	if err = os.WriteFile(filepath.Join(target, "outside.json"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	instanceDir := filepath.Dir(path)
	if err = os.Symlink(target, filepath.Join(instanceDir, "nested")); err != nil {
		t.Fatal(err)
	}
	err = instance.AtomicWrite(filepath.Join("nested", "state.json"), []byte("escape"), 0o600)
	if err == nil || atomicWriteCommitted(err) {
		t.Fatalf("escaping destination error = %v, want uncommitted failure", err)
	}
	data, readErr := os.ReadFile(filepath.Join(target, "outside.json"))
	if readErr != nil || string(data) != "outside" {
		t.Fatalf("pre-commit failure changed outside target: %q, %v", data, readErr)
	}
	leftovers, err = filepath.Glob(filepath.Join(instanceDir, "nested", ".state.json.tmp-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("failed atomic write leftovers = %v, %v", leftovers, err)
	}
}

func TestInstanceFSAtomicWriteCommittedAfterDirectorySyncFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	original := syncParentDirectoryAt
	syncParentDirectoryAt = func(*os.Root, string) error { return errors.New("sentinel sync failure") }
	t.Cleanup(func() { syncParentDirectoryAt = original })

	err = instance.AtomicWrite("state.json", []byte("committed"), 0o600)
	if err == nil || !atomicWriteCommitted(err) {
		t.Fatalf("AtomicWrite error = %v, want committed error", err)
	}
	data, readErr := os.ReadFile(filepath.Join(home, "instances", "cc-test", "state.json"))
	if readErr != nil || string(data) != "committed" {
		t.Fatalf("committed data = %q, %v", data, readErr)
	}
}
