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
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	original := readDirAt
	raced := false
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if rel == "." && !raced {
			raced = true
			// A writer slips an entry in after the scan but before the Remove.
			if writeErr := os.WriteFile(filepath.Join(dir, "late"), []byte("y"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	if err = instance.RemoveAll(); err != nil {
		t.Fatalf("RemoveAll did not recover from a concurrent write: %v", err)
	}
	if !raced {
		t.Fatal("test did not exercise the race")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("instance directory survived removal: %v", statErr)
	}
}

// An empty scan is not a reason to stop retrying: a writer that creates the
// first entry between the scan and the Remove is exactly the race the rescan
// exists for.
func TestRemoveAllRetriesAfterEmptyScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	original := readDirAt
	raced := false
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if rel == "." && !raced {
			raced = true
			if writeErr := os.WriteFile(filepath.Join(dir, "first"), []byte("y"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	if err = instance.RemoveAll(); err != nil {
		t.Fatalf("RemoveAll gave up after an empty scan raced a first write: %v", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("instance directory survived removal: %v", statErr)
	}
}

// A directory another process keeps refilling must surface the Remove error
// rather than retry forever.
func TestRemoveAllGivesUpOnUnendingWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	original := readDirAt
	scans := 0
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if rel == "." {
			scans++
			name := filepath.Join(dir, "late-"+strconv.Itoa(scans))
			if writeErr := os.WriteFile(name, []byte("y"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	if err = instance.RemoveAll(); err == nil {
		t.Fatal("RemoveAll should report the failed removal of a directory being refilled")
	}
	if scans > maxRemoveAllRescans+1 {
		t.Fatalf("RemoveAll rescanned %d times, want at most %d", scans, maxRemoveAllRescans+1)
	}
}

// The dialect name must not be resolved a second time during removal: a symlink
// to a sibling swapped in mid-removal would otherwise be followed, since its
// target stays inside the shared instances root.
func TestRemoveAllDoesNotReResolveTheDialectName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(home, "instances", "cc-sibling")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	// Same entry name as the dialect being removed: if the recursion re-resolves
	// "cc-test/state" by name after the swap, it lands here and deletes it.
	if err := os.WriteFile(filepath.Join(sibling, "state"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	original := readDirAt
	swapped := false
	// Fires on the first scan whatever path shape the removal uses, so the test
	// exercises the swap against any implementation rather than silently passing.
	readDirAt = func(scanned *os.Root, rel string) ([]os.DirEntry, error) {
		entries, readErr := original(scanned, rel)
		if !swapped {
			swapped = true
			// Between the scan and the removal, replace the dialect directory
			// with a link to its sibling.
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				t.Fatal(rmErr)
			}
			if linkErr := os.Symlink("cc-sibling", dir); linkErr != nil {
				t.Fatal(linkErr)
			}
		}
		return entries, readErr
	}
	t.Cleanup(func() { readDirAt = original })

	_ = instance.RemoveAll()
	if !swapped {
		t.Fatal("test did not exercise the swap")
	}
	if data, readErr := os.ReadFile(filepath.Join(sibling, "state")); readErr != nil || string(data) != "keep me" {
		t.Fatalf("removal followed the swapped link into a sibling dialect: %q, %v", data, readErr)
	}
}

// A spawned child re-opens the instance by name. If the directory that name
// resolves to is swapped after the parent pinned its root, the two are working
// in different places and the parent must notice.
func TestStillPinnedDetectsASwappedDialectDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dir := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if err = instance.AtomicWrite("proxy.pid", []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pinned, err := instance.StillPinned()
	if err != nil || !pinned {
		t.Fatalf("StillPinned() = %v, %v before any swap", pinned, err)
	}

	// Replace the directory with a different real one, as a rename would.
	if err = os.Rename(dir, filepath.Join(home, "instances", "cc-moved")); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, err = instance.StillPinned()
	if err != nil {
		t.Fatal(err)
	}
	if pinned {
		t.Fatal("StillPinned() did not notice the dialect directory was replaced")
	}
}

// The lexical RelUnder check passes for a path that merely reads as being inside
// the dialect; only comparing file identity catches a write that actually landed
// elsewhere.
func TestConfirmWrittenInsideRejectsALookalikePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	authDir := filepath.Join(home, "instances", "cc-test", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	confined := filepath.Join(authDir, "codex.json")
	if err = os.WriteFile(confined, []byte(`{"in":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = instance.ConfirmWrittenInside(filepath.Join("auth", "codex.json"), confined); err != nil {
		t.Fatalf("a genuinely confined file was rejected: %v", err)
	}

	// The pathname still passes RelUnder, but the bytes are a different file.
	outside := filepath.Join(t.TempDir(), "codex.json")
	if err = os.WriteFile(outside, []byte(`{"out":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, relErr := instance.RelUnder(confined, "auth"); relErr != nil {
		t.Fatalf("lexical check should accept the in-dialect pathname: %v", relErr)
	}
	if err = instance.ConfirmWrittenInside(filepath.Join("auth", "codex.json"), outside); err == nil {
		t.Fatal("a file outside the dialect passed the identity check")
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
