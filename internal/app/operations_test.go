package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDialectOperationsPreserveIdentityAndValidateBeforeStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 3)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	var stopped atomic.Int32
	service.stopRuntime = func(string, Dialect) error {
		stopped.Add(1)
		return nil
	}

	created, err := service.CreateDialect(DialectInput{Name: "cc-test", Preset: "codex", Effort: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Dialect.Port != base {
		t.Fatalf("unexpected create result: %#v", created)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	original := cfg.Dialects["cc-test"]
	if original.APIKey == "" {
		t.Fatal("create did not assign a private API key")
	}
	statePath := filepath.Join(home, "instances", "cc-test", "claude", "history.jsonl")
	if err = os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(statePath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err = service.UpdateDialect(DialectInput{
		Name: "cc-test", Preset: "codex-sol", Model: "override-model", Effort: true,
	}, created.Revision); err != nil {
		t.Fatal(err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("update stopped runtime %d times, want 1", stopped.Load())
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	updated := cfg.Dialects["cc-test"]
	if updated.APIKey != original.APIKey || updated.Port != original.Port {
		t.Fatalf("update changed dialect identity: before=%#v after=%#v", original, updated)
	}
	if data, readErr := os.ReadFile(statePath); readErr != nil || string(data) != "preserved" {
		t.Fatalf("update did not preserve instance state: %q, %v", data, readErr)
	}

	currentRevision, err := configRevision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.UpdateDialect(DialectInput{
		Name: "cc-test", Preset: "codex", Effort: true, EffortLevel: "impossible",
	}, currentRevision); err == nil {
		t.Fatal("invalid update succeeded")
	}
	if stopped.Load() != 1 {
		t.Fatal("runtime was stopped before replacement validation completed")
	}
	if _, err = service.UpdateDialect(DialectInput{Name: "cc-test", Preset: "codex", Effort: true}, created.Revision); err == nil {
		t.Fatal("stale revision update succeeded")
	} else {
		var operationErr *OperationError
		if !errors.As(err, &operationErr) || operationErr.Code != ErrorRevisionConflict {
			t.Fatalf("stale revision error = %v", err)
		}
	}
	if stopped.Load() != 1 {
		t.Fatal("revision conflict stopped the runtime")
	}
}

func TestDialectUpdateValidatesCustomUpstreamBeforeStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg := defaultConfig()
	cfg.Dialects["cc-custom"] = Dialect{Preset: "codex", Model: "original", Port: 43170, APIKey: "private", Effort: true, Concurrency: 3}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	revision, err := configRevision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	var stopped atomic.Int32
	service.stopRuntime = func(string, Dialect) error {
		stopped.Add(1)
		return nil
	}

	tests := []struct {
		name         string
		baseURL      string
		authTokenEnv string
	}{
		{name: "base URL only", baseURL: "https://api.example.com/v1"},
		{name: "token only", authTokenEnv: "EXAMPLE_API_KEY"},
		{name: "relative URL", baseURL: "api.example.com/v1", authTokenEnv: "EXAMPLE_API_KEY"},
		{name: "unsupported scheme", baseURL: "ftp://api.example.com/v1", authTokenEnv: "EXAMPLE_API_KEY"},
		{name: "URL user information", baseURL: "https://user:secret@api.example.com/v1", authTokenEnv: "EXAMPLE_API_KEY"},
		{name: "invalid token environment", baseURL: "https://api.example.com/v1", authTokenEnv: "9INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, updateErr := service.UpdateDialect(DialectInput{
				Name: "cc-custom", Model: "replacement", BaseURL: test.baseURL, AuthTokenEnv: test.authTokenEnv,
			}, revision)
			if updateErr == nil {
				t.Fatal("invalid custom upstream update succeeded")
			}
			var operationErr *OperationError
			if !errors.As(updateErr, &operationErr) || operationErr.Code != ErrorInvalidInput {
				t.Fatalf("custom upstream error = %v", updateErr)
			}
			if stopped.Load() != 0 {
				t.Fatalf("runtime stopped before custom upstream validation: %d", stopped.Load())
			}
		})
	}

	if _, err = service.UpdateDialect(DialectInput{
		Name: "cc-custom", Model: "replacement", BaseURL: "https://api.example.com/v1", AuthTokenEnv: "EXAMPLE_API_KEY",
	}, revision); err != nil {
		t.Fatalf("valid custom upstream update failed: %v", err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("valid custom upstream stopped runtime %d times, want 1", stopped.Load())
	}
}

func TestDialectViewPresetRoundTripDoesNotCoerceCustomUpstream(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Dialects["cc-gpt-custom"] = Dialect{
		Model: "gpt-4o-custom", BaseURL: "https://api.example.com/v1",
		AuthTokenEnv: "EXAMPLE_API_KEY", Port: 43170, APIKey: "private", Concurrency: 3,
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.stopRuntime = func(string, Dialect) error { return nil }

	view, revision, err := service.Dialect("cc-gpt-custom")
	if err != nil {
		t.Fatal(err)
	}
	// A custom upstream with no stored preset must not surface an inferred preset,
	// because the dashboard form submits that value back verbatim on save.
	if view.Preset != "custom" {
		t.Fatalf("custom dialect view preset = %q, want \"custom\"", view.Preset)
	}

	// Mimic the dashboard round-trip: inputFromView drops a "custom" preset to empty.
	submittedPreset := view.Preset
	if submittedPreset == "custom" {
		submittedPreset = ""
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-gpt-custom", Preset: submittedPreset, Model: view.Model,
		BaseURL: view.BaseURL, AuthTokenEnv: view.AuthTokenEnv, Concurrency: view.Concurrency,
	}, revision)
	if err != nil {
		t.Fatalf("round-trip update failed: %v", err)
	}
	if result.Dialect.AuthProvider != "" {
		t.Fatalf("round-trip injected auth provider %q into a custom dialect", result.Dialect.AuthProvider)
	}
	saved, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Dialects["cc-gpt-custom"]
	if got.Preset != "" || got.AuthProvider != "" {
		t.Fatalf("round-trip coerced custom dialect into preset %q / provider %q", got.Preset, got.AuthProvider)
	}
	if got.BaseURL != "https://api.example.com/v1" || got.AuthTokenEnv != "EXAMPLE_API_KEY" {
		t.Fatalf("round-trip dropped custom upstream: base=%q token=%q", got.BaseURL, got.AuthTokenEnv)
	}
}

func TestDialectMutationStopFailuresPreserveConfigurationAndState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg := defaultConfig()
	original := Dialect{Preset: "codex", Model: "old-model", Port: 43170, APIKey: "private-key", Effort: true, Concurrency: 3}
	cfg.Dialects["cc-stop-failure"] = original
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	instancePath := filepath.Join(home, "instances", "cc-stop-failure", "claude", "history.jsonl")
	if err := os.MkdirAll(filepath.Dir(instancePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instancePath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.stopRuntime = func(string, Dialect) error { return errors.New("sentinel stop failure") }

	if _, err := service.UpdateDialect(DialectInput{Name: "cc-stop-failure", Preset: "codex-sol", Effort: true}, ""); err == nil {
		t.Fatal("update succeeded after runtime stop failure")
	}
	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Dialects["cc-stop-failure"]; got.Model != original.Model || got.APIKey != original.APIKey {
		t.Fatalf("stop failure changed configuration: %#v", got)
	}
	if err = service.RemoveDialect("cc-stop-failure", ""); err == nil {
		t.Fatal("remove succeeded after runtime stop failure")
	}
	loaded, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Dialects["cc-stop-failure"]; !ok {
		t.Fatal("stop failure removed the dialect configuration")
	}
	if data, readErr := os.ReadFile(instancePath); readErr != nil || string(data) != "preserved" {
		t.Fatalf("stop failure removed instance state: %q, %v", data, readErr)
	}
}

func TestConcurrentDialectMutationsDoNotLoseUpdates(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	var wait sync.WaitGroup
	errorsCh := make(chan error, 2)
	for index, name := range []string{"cc-one", "cc-two"} {
		wait.Add(1)
		go func(index int, name string) {
			defer wait.Done()
			_, err := service.CreateDialect(DialectInput{
				Name: name, Preset: "codex", Port: base + index, Effort: true,
			}, "")
			errorsCh <- err
		}(index, name)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Dialects) != 2 {
		t.Fatalf("concurrent mutations left %d dialects, want 2", len(cfg.Dialects))
	}
}

func TestSafeDialectViewAndShowNeverExposeSecrets(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Dialects["cc-safe"] = Dialect{
		Preset: "codex", Model: "model", SubagentModel: "sub", Port: 43170,
		APIKey: "sentinel-local-api-key", AuthProvider: "codex",
		ExtraEnv: map[string]string{"SECRET_TOKEN": "sentinel-extra-env-value"},
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	view, _, err := newAppService().Dialect("cc-safe")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"sentinel-local-api-key", "sentinel-extra-env-value"} {
		if strings.Contains(text, secret) {
			t.Fatalf("safe DTO exposed %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "SECRET_TOKEN") {
		t.Fatalf("safe DTO did not expose the allowlisted environment key: %s", text)
	}
	output := captureStdout(t, func() error { return showDialect([]string{"cc-safe"}) })
	if strings.Contains(output, "sentinel-local-api-key") || strings.Contains(output, "sentinel-extra-env-value") || strings.Contains(output, "apiKey") {
		t.Fatalf("show exposed a secret: %s", output)
	}
}

func TestRestartStopsThenStartsLatestConfiguration(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Dialects["cc-restart"] = Dialect{Model: "latest-model", Port: 43170, APIKey: "key"}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	var calls []string
	service.stopRuntime = func(name string, dialect Dialect) error {
		calls = append(calls, "stop:"+name+":"+dialect.Model)
		return nil
	}
	service.startRuntime = func(name string, dialect Dialect) error {
		calls = append(calls, "start:"+name+":"+dialect.Model)
		return nil
	}
	service.proxyProbe = func(Dialect) bool { return true }
	status, err := service.RestartDialect("cc-restart")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "stop:cc-restart:latest-model,start:cc-restart:latest-model" {
		t.Fatalf("restart order = %v", calls)
	}
	if status.State != RuntimeRunning {
		t.Fatalf("restart status = %s", status.State)
	}
}

func TestStartDialectBlocksPartiallyAuthenticatedMixedDialect(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	dialect := presets["mixed-frontier"]
	dialect.Preset = "mixed-frontier"
	dialect.Port = 43170
	dialect.APIKey = "key"
	cfg := defaultConfig()
	cfg.Dialects["cc-mixed"] = dialect
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// Authenticate only one of the four providers the mapping requires.
	_, _, _, authDir, _, _, _, err := paths("cc-mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(authDir, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	started := false
	service.startRuntime = func(string, Dialect) error {
		started = true
		return nil
	}
	_, err = service.StartDialect("cc-mixed")
	if err == nil {
		t.Fatal("StartDialect should refuse a partially authenticated mixed dialect")
	}
	for _, provider := range []string{"claude", "kimi", "xai"} {
		if !strings.Contains(err.Error(), provider) {
			t.Fatalf("error should list missing provider %q, got %q", provider, err.Error())
		}
	}
	if started {
		t.Fatal("runtime must not start while providers are unauthenticated")
	}
}

func TestRuntimeStatusReportsDegradedComponents(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	dialect := presets["cursor-composer"]
	dialect.Port = 43170
	dialect.BridgePort = 43171
	dialect.APIKey = "private"
	cfg.Dialects["cc-cursor"] = dialect
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.proxyProbe = func(Dialect) bool { return true }
	service.bridgeProbe = func(Dialect) bool { return false }
	status, err := service.DialectStatus("cc-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != RuntimeDegraded || status.Proxy.State != RuntimeRunning || status.Bridge == nil || status.Bridge.State != RuntimeStopped {
		t.Fatalf("unexpected degraded status: %#v", status)
	}
	service.bridgeProbe = func(Dialect) bool { return true }
	status, err = service.DialectStatus("cc-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != RuntimeRunning {
		t.Fatalf("healthy components reported %s", status.State)
	}
}

func TestListDialectStatusUsesBoundedConcurrency(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	for index := range 8 {
		cfg.Dialects[fmt.Sprintf("cc-%d", index)] = Dialect{Model: "model", Port: 44000 + index, APIKey: "key"}
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.statusWorkers = 3
	var active atomic.Int32
	var maximum atomic.Int32
	service.proxyProbe = func(Dialect) bool {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		active.Add(-1)
		return false
	}
	started := time.Now()
	result, err := service.ListDialects(true)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if len(result.Dialects) != 8 {
		t.Fatalf("listed %d dialects, want 8", len(result.Dialects))
	}
	if maximum.Load() < 2 || maximum.Load() > 3 {
		t.Fatalf("maximum concurrent probes = %d, want 2..3", maximum.Load())
	}
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("status probes took %s; expected bounded concurrent execution", elapsed)
	}
}

func TestRemoveDialectDeletesOnlyValidatedMemberState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg := defaultConfig()
	cfg.Dialects["cc-remove"] = Dialect{Model: "model", Port: 43170, APIKey: "key"}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	instance := filepath.Join(home, "instances", "cc-remove")
	if err := os.MkdirAll(instance, 0o700); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.stopRuntime = func(string, Dialect) error { return nil }
	if err := service.RemoveDialect("../cc-remove", ""); err == nil {
		t.Fatal("remove accepted an unsafe name")
	}
	if _, err := os.Stat(instance); err != nil {
		t.Fatalf("unsafe remove touched instance state: %v", err)
	}
	if err := service.RemoveDialect("cc-remove", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(instance); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instance directory still exists: %v", err)
	}
}

func TestNativeLauncherTrackingAndHashProtection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	toolDir := filepath.Join(home, "tools")
	installDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(toolDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)
	service := newAppService()
	installed, err := service.InstallNativeLauncher(NativeLauncherInput{Name: "native-safe", Directory: installDir}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Launcher.Verified {
		t.Fatal("new launcher was not verified")
	}
	info, err := os.Stat(installed.Launcher.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("launcher mode = %o, want 755", info.Mode().Perm())
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	record := cfg.NativeLaunchers["native-safe"]
	if record.SHA256 == "" || record.ClaudePath != claudePath {
		t.Fatalf("launcher was not tracked safely: %#v", record)
	}
	if err = os.WriteFile(record.Path, []byte("externally modified\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = service.InstallNativeLauncher(NativeLauncherInput{Name: "native-safe", Directory: installDir, Dangerous: true}, ""); err == nil {
		t.Fatal("update overwrote an externally modified launcher")
	}
	if err = service.RemoveNativeLauncher("native-safe", ""); err == nil {
		t.Fatal("remove deleted an externally modified launcher")
	}
	if err = atomicWriteFile(record.Path, []byte(nativeLauncherBody(record.ClaudePath, record.Dangerous)), 0o755); err != nil {
		t.Fatal(err)
	}
	updated, err := service.InstallNativeLauncher(NativeLauncherInput{Name: "native-safe", Directory: installDir, Dangerous: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Launcher.Dangerous || !updated.Launcher.Verified {
		t.Fatalf("tracked launcher update failed: %#v", updated)
	}
	movedDir := filepath.Join(home, "moved-bin")
	if _, err = service.InstallNativeLauncher(NativeLauncherInput{Name: "native-safe", Directory: movedDir, Dangerous: true}, updated.Revision); err == nil {
		t.Fatal("tracked launcher path change succeeded without an explicit remove")
	} else {
		var operationErr *OperationError
		if !errors.As(err, &operationErr) || operationErr.Code != ErrorInvalidInput {
			t.Fatalf("path change error = %v", err)
		}
	}
	if _, err = os.Stat(updated.Launcher.Path); err != nil {
		t.Fatalf("immutable launcher path was disturbed: %v", err)
	}
	if _, err = os.Stat(movedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected path change created the destination directory: %v", err)
	}
	if err = service.RemoveNativeLauncher("native-safe", updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(updated.Launcher.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launcher still exists after removal: %v", err)
	}
}

func TestNativeLauncherDirectoryMustBeAbsoluteOrBlank(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", filepath.Join(home, "state"))
	t.Setenv("HOME", home)
	toolDir := filepath.Join(home, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(toolDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)
	service := newAppService()

	for _, directory := range []string{"bin", "./bin", "../bin", "~/.local/bin"} {
		t.Run(directory, func(t *testing.T) {
			_, err := service.InstallNativeLauncher(NativeLauncherInput{Name: "native-relative", Directory: directory}, "")
			if err == nil {
				t.Fatal("relative launcher directory succeeded")
			}
			var operationErr *OperationError
			if !errors.As(err, &operationErr) || operationErr.Code != ErrorInvalidInput {
				t.Fatalf("relative directory error = %v", err)
			}
		})
	}

	installed, err := service.InstallNativeLauncher(NativeLauncherInput{Name: "native-default"}, "")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, ".local", "bin", "native-default")
	if installed.Launcher.Path != wantPath {
		t.Fatalf("default launcher path = %q, want %q", installed.Launcher.Path, wantPath)
	}
}

func TestCursorStructuredStatusDoesNotExposeAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(binDir, "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\nprintf 'v22.13.1\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CURSOR_API_KEY", "sentinel-cursor-api-key")
	_, _, packagePath, err := cursorRuntimePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(packagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(packagePath, []byte(`{"version":"`+cursorSDKVersion+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status := inspectCursorRuntime()
	if status.NodeError != "" || !status.RuntimeInstalled || !status.RuntimeCurrent || !status.APIKeySet {
		t.Fatalf("unexpected Cursor status: %#v", status)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sentinel-cursor-api-key") {
		t.Fatalf("Cursor status exposed the API key: %s", raw)
	}
}

func TestConfigRevisionMigrationAndAtomicWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	legacy := `{"version":1,"basePort":43170,"dialects":{}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != configVersion || cfg.NativeLaunchers == nil {
		t.Fatalf("legacy config was not initialized: %#v", cfg)
	}
	before, err := configRevision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Dialects["cc-new"] = Dialect{Model: "model", Port: 43170, APIKey: "key"}
	if err = saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := configRevision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("configuration revision did not change")
	}
	leftovers, err := filepath.Glob(filepath.Join(home, ".config.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("atomic write left temporary files: %v", leftovers)
	}
}

func TestAtomicWriteMarksPostRenameSyncErrorsCommitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	originalSync := syncParentDirectory
	syncParentDirectory = func(string) error { return errors.New("sentinel directory sync failure") }
	defer func() { syncParentDirectory = originalSync }()

	err := atomicWriteFile(path, []byte("committed"), 0o600)
	if err == nil || !atomicWriteCommitted(err) {
		t.Fatalf("atomic write error = %v, want committed post-rename error", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "committed" {
		t.Fatalf("renamed data was not committed: %q, %v", data, readErr)
	}
}

func TestNativeInstallDoesNotRollbackCommittedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	toolDir := filepath.Join(home, "tools")
	installDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)
	originalSync := syncParentDirectory
	syncParentDirectory = func(dir string) error {
		if filepath.Clean(dir) == filepath.Clean(home) {
			return errors.New("sentinel config directory sync failure")
		}
		return originalSync(dir)
	}
	defer func() { syncParentDirectory = originalSync }()

	_, err := newAppService().InstallNativeLauncher(NativeLauncherInput{Name: "native-committed", Directory: installDir}, "")
	if err == nil || !atomicWriteCommitted(err) {
		t.Fatalf("install error = %v, want committed config error", err)
	}
	cfg, loadErr := loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	record, ok := cfg.NativeLaunchers["native-committed"]
	if !ok {
		t.Fatal("committed config entry was rolled back")
	}
	if verifyErr := verifyNativeLauncher(record); verifyErr != nil {
		t.Fatalf("committed launcher was rolled back: %v", verifyErr)
	}
}

func TestNativeInstallTracksCommittedLauncherWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	toolDir := filepath.Join(home, "tools")
	installDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)
	originalSync := syncParentDirectory
	syncParentDirectory = func(dir string) error {
		if filepath.Clean(dir) == filepath.Clean(installDir) {
			return errors.New("sentinel launcher directory sync failure")
		}
		return originalSync(dir)
	}
	defer func() { syncParentDirectory = originalSync }()

	result, err := newAppService().InstallNativeLauncher(NativeLauncherInput{Name: "native-launcher-committed", Directory: installDir}, "")
	if err == nil || !atomicWriteCommitted(err) {
		t.Fatalf("install error = %v, want committed launcher error", err)
	}
	if !result.Launcher.Verified {
		t.Fatalf("committed launcher was not returned as tracked: %#v", result)
	}
	cfg, loadErr := loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := cfg.NativeLaunchers["native-launcher-committed"]; !ok {
		t.Fatal("committed launcher write was left untracked")
	}
}

func TestNativeRemoveDoesNotRestoreAfterCommittedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	toolDir := filepath.Join(home, "tools")
	installDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)
	service := newAppService()
	installed, err := service.InstallNativeLauncher(NativeLauncherInput{Name: "native-remove", Directory: installDir}, "")
	if err != nil {
		t.Fatal(err)
	}
	originalSync := syncParentDirectory
	syncParentDirectory = func(dir string) error {
		if filepath.Clean(dir) == filepath.Clean(home) {
			return errors.New("sentinel config directory sync failure")
		}
		return originalSync(dir)
	}
	defer func() { syncParentDirectory = originalSync }()

	err = service.RemoveNativeLauncher("native-remove", installed.Revision)
	if err == nil || !atomicWriteCommitted(err) {
		t.Fatalf("remove error = %v, want committed config error", err)
	}
	cfg, loadErr := loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := cfg.NativeLaunchers["native-remove"]; ok {
		t.Fatal("committed launcher removal was rolled back in config")
	}
	if _, statErr := os.Stat(installed.Launcher.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("committed launcher removal restored the file: %v", statErr)
	}
}

func TestStateLockSerializesAcrossProcesses(t *testing.T) {
	if os.Getenv("CC_DIALECT_STATE_LOCK_HELPER") == "1" {
		if err := withStateLock(func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		return
	}
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	var command *exec.Cmd
	var done chan error
	err := withStateLock(func() error {
		command = exec.Command(os.Args[0], "-test.run=^TestStateLockSerializesAcrossProcesses$")
		command.Env = append(os.Environ(), "CC_DIALECT_STATE_LOCK_HELPER=1", "DIALECT_HOME="+home)
		if startErr := command.Start(); startErr != nil {
			return startErr
		}
		done = make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case waitErr := <-done:
			return fmt.Errorf("helper passed the held state lock early: %w", waitErr)
		case <-time.After(150 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("helper did not acquire the state lock after release")
	}
}

func captureStdout(t *testing.T, operation func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	var buffer strings.Builder
	var copyErr error
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		_, copyErr = io.Copy(&buffer, reader)
	}()
	operationErr := operation()
	_ = writer.Close()
	os.Stdout = original
	wait.Wait()
	_ = reader.Close()
	if operationErr != nil {
		t.Fatal(operationErr)
	}
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	return buffer.String()
}
