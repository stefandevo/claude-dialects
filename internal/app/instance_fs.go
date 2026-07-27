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
// the instances root. Absolute paths are exposed only for user-facing messages
// and external APIs that require pathname strings.
type instanceFS struct {
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
	root, err := instancesRoot()
	if err != nil {
		return nil, err
	}
	return &instanceFS{root: root, name: name, instancesDir: filepath.Join(home, "instances")}, nil
}

func (instance *instanceFS) Close() error {
	return instance.root.Close()
}

func (instance *instanceFS) path(rel string) (string, error) {
	if rel == "." {
		return instance.name, nil
	}
	if !filepath.IsLocal(rel) {
		return "", operationError(ErrorInvalidInput, "invalid instance-relative path %q", rel)
	}
	return filepath.Join(instance.name, rel), nil
}

func (instance *instanceFS) Abs(rel string) (string, error) {
	path, err := instance.path(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(instance.instancesDir, path), nil
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
	return instance.root.ReadFile(path)
}

func (instance *instanceFS) ReadDir(rel string) ([]os.DirEntry, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	directory, err := instance.root.Open(path)
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
	return instance.root.Open(path)
}

func (instance *instanceFS) OpenAppend(rel string, mode os.FileMode) (*os.File, error) {
	path, err := instance.path(rel)
	if err != nil {
		return nil, err
	}
	if err = instance.root.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return instance.root.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
}

func (instance *instanceFS) MkdirAll(rel string, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	return instance.root.MkdirAll(path, mode)
}

func (instance *instanceFS) Chmod(rel string, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	return rootChmod(instance.root, path, mode)
}

func (instance *instanceFS) Remove(rel string) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	return instance.root.Remove(path)
}

func (instance *instanceFS) RemoveIfExists(rel string) error {
	err := instance.Remove(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (instance *instanceFS) RemoveAll() error {
	return removeAllAt(instance.root, instance.name)
}

func (instance *instanceFS) AtomicWrite(rel string, data []byte, mode os.FileMode) error {
	path, err := instance.path(rel)
	if err != nil {
		return err
	}
	return atomicWriteFileAt(instance.root, path, data, mode)
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
