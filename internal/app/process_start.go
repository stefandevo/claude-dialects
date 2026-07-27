package app

import (
	"errors"
	"os"
	"os/exec"
)

// monitorStartedProcess reaps a started child without blocking startup. The
// buffered channel lets the waiter finish even after the caller has returned
// successfully and no longer needs the exit status.
func monitorStartedProcess(cmd *exec.Cmd) <-chan error {
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
	}()
	return exited
}

// cleanupStartedProcess terminates and reaps a child before removing its PID
// sidecar. If termination fails, the PID remains so the running process is not
// left without its ownership record.
func cleanupStartedProcess(cmd *exec.Cmd, exited <-chan error, instance *instanceFS, pidFile string) error {
	killErr := cmd.Process.Kill()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return killErr
	}
	<-exited
	return instance.RemoveIfExists(pidFile)
}
