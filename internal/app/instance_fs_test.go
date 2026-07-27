package app

import (
	"errors"
	"os"
	"path/filepath"
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
