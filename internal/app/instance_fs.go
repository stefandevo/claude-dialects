package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// instanceIdentityEnv carries the parent's pinned dialect-directory identity to
// the __proxy child. It travels in the environment rather than as an argument so
// the internal command's positional contract is unchanged.
const instanceIdentityEnv = "CC_DIALECT_INSTANCE_IDENTITY"

// instanceFS confines repository-owned filesystem operations for one dialect to
// that dialect's own directory. Absolute paths are exposed only for user-facing
// messages and external APIs that require pathname strings.
//
// Rooting at <instances>/<name> rather than at <instances> is what makes the
// dialects isolated from each other: os.Root follows a symlink whose target
// stays inside the root, so against the shared root a link such as
// cc-a/auth -> ../cc-b/auth resolves and one dialect reads another's
// credentials, config or history. Only a per-dialect root refuses it.
type instanceFS struct {
	// parent is the shared instances root. It is the boundary for operations on
	// the dialect entry itself — chiefly removal, which must unlink a symlinked
	// instance rather than resolve it.
	parent *os.Root
	// root is the dialect's own directory, opened lazily by dir() so that
	// constructing an instanceFS neither requires the directory to exist nor
	// creates it as a side effect of a read.
	root *os.Root
	// pinErr records that Pin already resolved this name and rejected what it
	// found. It is sticky: dir returns it instead of resolving the name a second
	// time, so every later operation on this handle refuses the entry the pin
	// refused rather than acting on whatever has since taken its place. Removal
	// is the one exception, and it unlinks that entry without following it.
	pinErr       error
	name         string
	instancesDir string
}

func openInstanceFS(name string) (*instanceFS, error) {
	if !validName(name) {
		return nil, operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	parent, err := instancesRoot()
	if err != nil {
		return nil, err
	}
	return &instanceFS{parent: parent, name: name, instancesDir: filepath.Join(home, "instances")}, nil
}

func (instance *instanceFS) Close() error {
	err := instance.parent.Close()
	if instance.root != nil {
		err = errors.Join(instance.root.Close(), err)
	}
	return err
}

// dir opens the dialect's own directory as a root, refusing a symlinked or
// missing instance. Callers that only read report the underlying error; callers
// that write go through ensureDir so the directory is created first.
//
// A pin that was already refused is reported again rather than retried. Asking
// a second time would resolve the name afresh, so a real directory moved into
// it since — a sibling dialect, say — would be read, written or signalled by
// operations that only reached this point because the pin let them.
func (instance *instanceFS) dir() (*os.Root, error) {
	if instance.root != nil {
		return instance.root, nil
	}
	if instance.pinErr != nil {
		return nil, instance.pinErr
	}
	root, err := openRootChild(instance.parent, instance.name, filepath.Join(instance.instancesDir, instance.name))
	if err != nil {
		return nil, err
	}
	instance.root = root
	return root, nil
}

// ensureDir creates the dialect directory when absent, then opens it as a root.
// A pre-existing symlink is still refused: MkdirAll accepts a link resolving
// inside the instances root, but dir's validation rejects it.
func (instance *instanceFS) ensureDir() (*os.Root, error) {
	if instance.root == nil {
		if err := instance.parent.MkdirAll(instance.name, 0o700); err != nil {
			return nil, err
		}
	}
	return instance.dir()
}

func (instance *instanceFS) path(rel string) (string, error) {
	if rel == "." {
		return ".", nil
	}
	if !filepath.IsLocal(rel) {
		return "", operationError(ErrorInvalidInput, "invalid instance-relative path %q", rel)
	}
	return rel, nil
}

func (instance *instanceFS) Abs(rel string) (string, error) {
	path, err := instance.path(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(instance.instancesDir, instance.name, path), nil
}

// RelUnder converts an absolute path handed back by an external dependency into
// a path relative to this dialect, rejecting anything that does not land under
// the named subdirectory. The result still goes through the dialect root, so
// this is a check on where the dependency claims to have written, not the
// confinement itself.
func (instance *instanceFS) RelUnder(abs, parent string) (string, error) {
	base := filepath.Join(instance.instancesDir, instance.name)
	rel, err := filepath.Rel(base, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", operationError(ErrorInvalidInput, "path %q is outside dialect %q", abs, instance.name)
	}
	if rel != parent && !strings.HasPrefix(rel, parent+string(filepath.Separator)) {
		return "", operationError(ErrorInvalidInput, "path %q is outside %s in dialect %q", abs, parent, instance.name)
	}
	return rel, nil
}

func (instance *instanceFS) ReadFile(rel string) ([]byte, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	root, err := instance.dir()
	if err != nil {
		return nil, err
	}
	return root.ReadFile(path)
}

func (instance *instanceFS) ReadDir(rel string) ([]os.DirEntry, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	root, err := instance.dir()
	if err != nil {
		return nil, err
	}
	return readDirAt(root, path)
}

func (instance *instanceFS) Open(rel string) (*os.File, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	root, err := instance.dir()
	if err != nil {
		return nil, err
	}
	return root.Open(path)
}

func (instance *instanceFS) OpenAppend(rel string, mode os.FileMode) (*os.File, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	root, err := instance.ensureDir()
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err = root.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	return root.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
}

func (instance *instanceFS) MkdirAll(rel string, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	root, err := instance.ensureDir()
	if err != nil {
		return err
	}
	return root.MkdirAll(path, mode)
}

func (instance *instanceFS) Chmod(rel string, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	root, err := instance.dir()
	if err != nil {
		return err
	}
	return rootChmod(root, path, mode)
}

func (instance *instanceFS) Remove(rel string) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	root, err := instance.dir()
	if err != nil {
		return err
	}
	return root.Remove(path)
}

func (instance *instanceFS) RemoveIfExists(rel string) error {
	err := instance.Remove(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RemoveAll deletes the whole dialect directory.
//
// The contents go through the pinned per-dialect root, so the dialect name is
// never resolved a second time: re-opening it by name after checking it is what
// would let a symlink swapped in mid-removal redirect the recursion into a
// sibling dialect, since that target stays inside the shared instances root.
// Only the final unlink uses the parent, and Remove does not follow symlinks —
// which is precisely why a symlinked instance is unlinked here rather than
// resolved, and why removal is the one operation a symlinked instance survives.
func (instance *instanceFS) RemoveAll() error {
	if instance.pinErr != nil {
		// Pin already looked and refused: the entry was missing, a symlink, or not
		// a directory. dir would now report that refusal rather than resolve the
		// name again, and there is nothing to descend into anyway — so unlink the
		// entry the refusal was about, and let a non-empty directory that has since
		// replaced it fail the unlink rather than be erased.
		return instance.unlinkEntry()
	}
	root, dirErr := instance.dir()
	if dirErr != nil {
		// Missing, a symlink, or not a directory: nothing to descend into, so
		// unlink whatever the entry is.
		return instance.unlinkEntry()
	}
	defer func() {
		_ = root.Close()
		instance.root = nil
	}()
	for pass := 0; ; pass++ {
		if err := removeAllUnder(root, "."); err != nil {
			return err
		}
		err := instance.unlinkEntry()
		if err == nil || pass >= maxRemoveAllRescans {
			return err
		}
	}
}

func (instance *instanceFS) unlinkEntry() error {
	err := instance.parent.Remove(instance.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (instance *instanceFS) AtomicWrite(rel string, data []byte, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	root, err := instance.ensureDir()
	if err != nil {
		return err
	}
	return atomicWriteFileAt(root, path, data, mode)
}

// Pin resolves the dialect directory now, so subsequent operations act on this
// directory rather than on whatever the name resolves to at each later moment.
// It reports whether a directory was pinned: an entry that is not a real
// directory stays unpinned, which removal relies on to unlink it without
// following it.
func (instance *instanceFS) Pin() bool {
	_, instance.pinErr = instance.dir()
	return instance.pinErr == nil
}

// Identity returns a serialisable identity for the pinned directory — the
// device and inode it resolved to. os.SameFile cannot cross a process boundary,
// so this is what the parent hands a spawned child to let it confirm that the
// dialect name it re-resolved landed on the same directory the parent pinned.
func (instance *instanceFS) Identity() (string, error) {
	root, err := instance.dir()
	if err != nil {
		return "", err
	}
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("cannot identify the directory for dialect %q on this platform", instance.name)
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}

// MatchesIdentity reports whether the pinned directory is the one identified by
// expected, as produced by Identity in another process.
func (instance *instanceFS) MatchesIdentity(expected string) error {
	actual, err := instance.Identity()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("dialect %q now resolves to a different directory than the one that started this process", instance.name)
	}
	return nil
}

// StillPinned reports whether this handle's directory is still the one the
// dialect name resolves to. A spawned child re-opens the instance by name, so a
// rename between the parent's open and the child's leaves the two working in
// different directories — the child serving from one while the parent records
// its PID in the other.
func (instance *instanceFS) StillPinned() (bool, error) {
	root, err := instance.dir()
	if err != nil {
		return false, err
	}
	pinned, err := root.Stat(".")
	if err != nil {
		return false, err
	}
	current, err := openInstanceFS(instance.name)
	if err != nil {
		return false, err
	}
	defer current.Close()
	currentRoot, err := current.dir()
	if err != nil {
		return false, err
	}
	live, err := currentRoot.Stat(".")
	if err != nil {
		return false, err
	}
	return os.SameFile(pinned, live), nil
}

// ConfirmWrittenInside reports whether abs — a pathname an external dependency
// wrote — is the very file rel resolves to through the pinned root. RelUnder
// only compares strings, and a string cannot distinguish a path inside the
// dialect from one that merely reads like it: if a component was replaced while
// the dependency held the pathname, the returned path still looks right while
// the bytes landed somewhere else. Comparing the identity of the two files is
// what tells them apart.
func (instance *instanceFS) ConfirmWrittenInside(rel, abs string) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	root, err := instance.dir()
	if err != nil {
		return err
	}
	confined, err := root.Stat(path)
	if err != nil {
		return err
	}
	// Lstat: a symlink at the final component is not the file we confined, so it
	// must fail the comparison rather than be followed to something that passes.
	written, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if !os.SameFile(confined, written) {
		return fmt.Errorf("%s is not the file %s resolves to inside dialect %q", abs, rel, instance.name)
	}
	return nil
}

// ReadPID returns the recorded PID, or 0 when no PID file exists. A file that
// exists but cannot be read safely — because the instance or the path itself was
// replaced with something the root refuses to follow — returns an error rather
// than 0: callers that stop or remove a runtime must be able to tell "nothing is
// running" apart from "the ownership record is unreadable", since treating the
// second as the first abandons a live process with no way to find it again.
func (instance *instanceFS) ReadPID(rel string) (int, error) {
	raw, err := instance.ReadFile(rel)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid, nil
}

// runningPID reports the PID for callers that only need to know whether this
// dialect looks alive and have nothing to abandon if the answer is wrong.
func (instance *instanceFS) runningPID(rel string) int {
	pid, err := instance.ReadPID(rel)
	if err != nil {
		return 0
	}
	return pid
}

func (instance *instanceFS) WritePID(rel string, pid int) error {
	return instance.AtomicWrite(rel, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func (instance *instanceFS) WriteBuildIdentity(rel string) error {
	return instance.AtomicWrite(rel, []byte(appBuildIdentity()+"\n"), 0o600)
}
