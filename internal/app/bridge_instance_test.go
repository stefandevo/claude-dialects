package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBridgePIDReadersRejectSymlinkedInstance(t *testing.T) {
	_, target := symlinkedInstance(t, "cc-test")
	for _, file := range []string{"cursor-bridge.pid", "copilot-bridge.pid"} {
		if err := os.WriteFile(filepath.Join(target, file), []byte("4242\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := cursorBridgePID("cc-test"); got != 0 {
		t.Fatalf("cursorBridgePID read %d through an escaping symlink", got)
	}
	if got := copilotBridgePID("cc-test"); got != 0 {
		t.Fatalf("copilotBridgePID read %d through an escaping symlink", got)
	}
}

func TestBridgeStopDoesNotRemoveExternalPID(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		bridge string
		stop   func(string, Dialect) error
	}{
		{name: "Cursor", file: "cursor-bridge.pid", bridge: "cursor", stop: stopCursorBridge},
		{name: "Copilot", file: "copilot-bridge.pid", bridge: "copilot", stop: stopCopilotBridge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, target := symlinkedInstance(t, "cc-test")
			pidPath := filepath.Join(target, test.file)
			if err := os.WriteFile(pidPath, []byte("4242\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := test.stop("cc-test", Dialect{Bridge: test.bridge, BridgePort: 1, APIKey: "key"}); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(pidPath); err != nil || string(data) != "4242\n" {
				t.Fatalf("stop touched external PID: %q, %v", data, err)
			}
		})
	}
}

func TestBridgePreparationRejectsSymlinkedInstance(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*instanceFS) (string, *os.File, error)
	}{
		{name: "Cursor", prepare: prepareCursorBridgeFiles},
		{name: "Copilot", prepare: prepareCopilotBridgeFiles},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, target := symlinkedInstance(t, "cc-test")
			instance, err := openInstanceFS("cc-test")
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			_, logFile, err := test.prepare(instance)
			if logFile != nil {
				_ = logFile.Close()
			}
			if err == nil {
				t.Fatal("bridge preparation should reject a symlinked instance")
			}
			entries, readErr := os.ReadDir(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if entry.Name() != "cursor-bridge.pid" && entry.Name() != "copilot-bridge.pid" {
					t.Fatalf("bridge preparation wrote through symlink: %s", entry.Name())
				}
			}
		})
	}
}

func TestBridgePreparationReturnsRootedStatePath(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	workspace, cursorLog, err := prepareCursorBridgeFiles(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = cursorLog.Close(); err != nil {
		t.Fatal(err)
	}
	if want, _ := instance.Abs("cursor-workspace"); workspace != want {
		t.Fatalf("Cursor workspace = %q, want %q", workspace, want)
	}
	state, copilotLog, err := prepareCopilotBridgeFiles(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = copilotLog.Close(); err != nil {
		t.Fatal(err)
	}
	if want, _ := instance.Abs("copilot-home"); state != want {
		t.Fatalf("Copilot state = %q, want %q", state, want)
	}
	for _, rel := range []string{"cursor-workspace", "copilot-home"} {
		abs, _ := instance.Abs(rel)
		info, statErr := os.Stat(abs)
		if statErr != nil {
			t.Fatalf("prepared directory %s: %v", rel, statErr)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("prepared directory %s mode = %v, want drwx------", rel, info.Mode())
		}
	}
	for _, rel := range []string{"cursor-bridge.log", "copilot-bridge.log"} {
		abs, _ := instance.Abs(rel)
		info, statErr := os.Stat(abs)
		if statErr != nil {
			t.Fatalf("prepared log %s: %v", rel, statErr)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("prepared log %s mode = %v, want -rw-------", rel, info.Mode())
		}
	}
}
