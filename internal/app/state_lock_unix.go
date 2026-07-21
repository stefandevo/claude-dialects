package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

var stateMutationMutex sync.Mutex

func withStateLock(operation func() error) error {
	stateMutationMutex.Lock()
	defer stateMutationMutex.Unlock()

	home, err := homeDir()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(home, ".state.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	defer lockFile.Close()
	if err = os.Chmod(lockPath, 0o600); err != nil {
		return fmt.Errorf("secure state lock: %w", err)
	}
	if err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	return operation()
}
