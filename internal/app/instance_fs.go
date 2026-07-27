package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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
	root         *os.Root
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
func (instance *instanceFS) dir() (*os.Root, error) {
	if instance.root != nil {
		return instance.root, nil
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

func (instance *instanceFS) Rel(abs string) (string, error) {
	base := filepath.Join(instance.instancesDir, instance.name)
	rel, err := filepath.Rel(base, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", operationError(ErrorInvalidInput, "path %q is outside dialect %q", abs, instance.name)
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
	directory, err := instance.Open(rel)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	return entries, errors.Join(readErr, closeErr)
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

// RemoveAll deletes the whole dialect directory. It works from the parent root
// because the entry being removed is the dialect directory itself: a symlinked
// instance must be unlinked here, not resolved, so this is deliberately the one
// operation that does not go through the per-dialect root.
func (instance *instanceFS) RemoveAll() error {
	if instance.root != nil {
		if err := instance.root.Close(); err != nil {
			return err
		}
		instance.root = nil
	}
	return removeAllAt(instance.parent, instance.name)
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

func (instance *instanceFS) ReadPID(rel string) int {
	raw, err := instance.ReadFile(rel)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func (instance *instanceFS) WritePID(rel string, pid int) error {
	return instance.AtomicWrite(rel, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func (instance *instanceFS) WriteBuildIdentity(rel string) error {
	return instance.AtomicWrite(rel, []byte(appBuildIdentity()+"\n"), 0o600)
}

func (instance *instanceFS) ValidateUnder(rel, parent string) error {
	if rel == parent || strings.HasPrefix(rel, parent+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("path %q is outside %s", rel, parent)
}
