package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type OperationErrorCode string

const (
	ErrorInvalidInput       OperationErrorCode = "invalid_input"
	ErrorNotFound           OperationErrorCode = "not_found"
	ErrorAlreadyExists      OperationErrorCode = "already_exists"
	ErrorRevisionConflict   OperationErrorCode = "revision_conflict"
	ErrorExternallyModified OperationErrorCode = "externally_modified"
)

type OperationError struct {
	Code    OperationErrorCode
	Message string
}

func (e *OperationError) Error() string { return e.Message }

func operationError(code OperationErrorCode, format string, args ...any) error {
	return &OperationError{Code: code, Message: fmt.Sprintf(format, args...)}
}

type DialectInput struct {
	Name          string
	Preset        string
	Model         string
	SubagentModel string
	OpusModel     string
	SonnetModel   string
	HaikuModel    string
	EffortLevel   string
	Concurrency   int
	Port          int
	BridgePort    int
	BaseURL       string
	AuthTokenEnv  string
	Effort        bool
	ToolSearch    bool
}

type DialectView struct {
	Name          string   `json:"name"`
	Preset        string   `json:"preset"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	SubagentModel string   `json:"subagentModel,omitempty"`
	OpusModel     string   `json:"opusModel,omitempty"`
	SonnetModel   string   `json:"sonnetModel,omitempty"`
	HaikuModel    string   `json:"haikuModel,omitempty"`
	Effort        bool     `json:"effort"`
	EffortLevel   string   `json:"effortLevel,omitempty"`
	Concurrency   int      `json:"concurrency"`
	ToolSearch    bool     `json:"toolSearch"`
	Port          int      `json:"port"`
	BaseURL       string   `json:"baseUrl,omitempty"`
	AuthTokenEnv  string   `json:"authTokenEnv,omitempty"`
	AuthProvider  string   `json:"authProvider,omitempty"`
	AuthProviders []string `json:"authProviders,omitempty"`
	// UnauthenticatedProviders lists expected OAuth providers still missing
	// credentials, so the dashboard can prompt for the remaining logins.
	UnauthenticatedProviders []string       `json:"unauthenticatedProviders,omitempty"`
	Bridge                   string         `json:"bridge,omitempty"`
	BridgePort               int            `json:"bridgePort,omitempty"`
	ExtraEnvKeys             []string       `json:"extraEnvKeys,omitempty"`
	Status                   *RuntimeStatus `json:"status,omitempty"`
}

type DialectMutationResult struct {
	Dialect  DialectView `json:"dialect"`
	Created  bool        `json:"created"`
	Revision string      `json:"revision"`
}

type DialectListResult struct {
	Dialects []DialectView `json:"dialects"`
	Revision string        `json:"revision"`
}

type RuntimeState string

const (
	RuntimeRunning  RuntimeState = "running"
	RuntimeStopped  RuntimeState = "stopped"
	RuntimeDegraded RuntimeState = "degraded"
)

type ComponentStatus struct {
	State RuntimeState `json:"state"`
	PID   int          `json:"pid,omitempty"`
	Port  int          `json:"port,omitempty"`
}

type RuntimeStatus struct {
	State  RuntimeState     `json:"state"`
	Proxy  ComponentStatus  `json:"proxy"`
	Bridge *ComponentStatus `json:"bridge,omitempty"`
}

type NativeLauncherInput struct {
	Name      string
	Directory string
	Dangerous bool
}

type NativeLauncherView struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	ClaudePath string `json:"claudePath"`
	Dangerous  bool   `json:"dangerous"`
	Verified   bool   `json:"verified"`
}

type NativeLauncherResult struct {
	Launcher NativeLauncherView `json:"launcher"`
	Revision string             `json:"revision"`
}

type appService struct {
	proxyProbe      func(Dialect) bool
	bridgeProbe     func(Dialect) bool
	startRuntime    func(string, Dialect) error
	stopRuntime     func(string, Dialect) error
	statusWorkers   int
	cursorStatusFn  func() CursorRuntimeStatus
	cursorInstallFn func() (CursorInstallResult, error)
}

func newAppService() *appService {
	return &appService{
		proxyProbe:      proxyHealthy,
		bridgeProbe:     managedBridgeHealthy,
		startRuntime:    startProxy,
		stopRuntime:     stopProxyDialect,
		statusWorkers:   4,
		cursorStatusFn:  inspectCursorRuntime,
		cursorInstallFn: installCursorRuntime,
	}
}

func safeDialectView(name string, dialect Dialect) DialectView {
	// Expose only the stored preset here: it round-trips through the dashboard
	// form back into mutations, so an inferred preset (e.g. "codex" for a custom
	// gpt-* upstream) would coerce a custom dialect into a preset on save. The
	// inferred label is surfaced separately via Provider for display.
	preset := dialect.Preset
	if preset == "" {
		preset = "custom"
	}
	provider := providerForDialect(dialect)
	if provider == "" {
		provider = "custom"
	}
	extraEnvKeys := make([]string, 0, len(dialect.ExtraEnv))
	for key := range dialect.ExtraEnv {
		extraEnvKeys = append(extraEnvKeys, key)
	}
	sort.Strings(extraEnvKeys)
	return DialectView{
		Name: name, Preset: preset, Provider: provider,
		Model: dialect.Model, SubagentModel: dialect.SubagentModel,
		OpusModel: dialect.OpusModel, SonnetModel: dialect.SonnetModel, HaikuModel: dialect.HaikuModel,
		Effort: dialect.Effort, EffortLevel: dialect.EffortLevel, Concurrency: dialect.Concurrency,
		ToolSearch: dialect.ToolSearch, Port: dialect.Port, BaseURL: dialect.BaseURL,
		AuthTokenEnv: dialect.AuthTokenEnv, AuthProvider: dialect.AuthProvider,
		AuthProviders: expectedAuthProviders(dialect),
		Bridge:        dialect.Bridge, BridgePort: dialect.BridgePort, ExtraEnvKeys: extraEnvKeys,
	}
}

// installedDialectView builds a view for a configured dialect, adding the
// on-disk authentication status that only makes sense for a real instance —
// unlike safeDialectView, which is also reused to describe preset templates.
func installedDialectView(name string, dialect Dialect) DialectView {
	view := safeDialectView(name, dialect)
	view.UnauthenticatedProviders = missingAuthProviders(name, dialect)
	return view
}

func (service *appService) ListDialects(includeStatus bool) (DialectListResult, error) {
	cfg, err := loadConfig()
	if err != nil {
		return DialectListResult{}, err
	}
	revision, err := configRevision(cfg)
	if err != nil {
		return DialectListResult{}, err
	}
	names := make([]string, 0, len(cfg.Dialects))
	for name := range cfg.Dialects {
		names = append(names, name)
	}
	sort.Strings(names)
	result := DialectListResult{Dialects: make([]DialectView, len(names)), Revision: revision}
	if !includeStatus {
		for index, name := range names {
			result.Dialects[index] = installedDialectView(name, cfg.Dialects[name])
		}
		return result, nil
	}
	workers := service.statusWorkers
	if workers < 1 {
		workers = 1
	}
	if workers > len(names) {
		workers = len(names)
	}
	jobs := make(chan int)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				name := names[index]
				dialect := cfg.Dialects[name]
				view := installedDialectView(name, dialect)
				status := service.runtimeStatus(name, dialect)
				view.Status = &status
				result.Dialects[index] = view
			}
		}()
	}
	for index := range names {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	return result, nil
}

func (service *appService) Dialect(name string) (DialectView, string, error) {
	if !validName(name) {
		return DialectView{}, "", operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	cfg, err := loadConfig()
	if err != nil {
		return DialectView{}, "", err
	}
	dialect, ok := cfg.Dialects[name]
	if !ok {
		return DialectView{}, "", operationError(ErrorNotFound, "dialect %q does not exist", name)
	}
	revision, err := configRevision(cfg)
	return installedDialectView(name, dialect), revision, err
}

func (service *appService) CreateDialect(input DialectInput, expectedRevision string) (DialectMutationResult, error) {
	return service.mutateDialect(input, expectedRevision, false, true)
}

func (service *appService) UpdateDialect(input DialectInput, expectedRevision string) (DialectMutationResult, error) {
	return service.mutateDialect(input, expectedRevision, true, false)
}

func (service *appService) UpsertDialect(input DialectInput, expectedRevision string) (DialectMutationResult, error) {
	return service.mutateDialect(input, expectedRevision, false, false)
}

func (service *appService) mutateDialect(input DialectInput, expectedRevision string, requireExisting, requireMissing bool) (result DialectMutationResult, err error) {
	if !validName(input.Name) {
		return result, operationError(ErrorInvalidInput, "create requires a lowercase command name (letters, numbers, - or _)")
	}
	err = withStateLock(func() error {
		cfg, loadErr := loadConfig()
		if loadErr != nil {
			return loadErr
		}
		if revisionErr := requireConfigRevision(cfg, expectedRevision); revisionErr != nil {
			return revisionErr
		}
		existing, exists := cfg.Dialects[input.Name]
		if requireExisting && !exists {
			return operationError(ErrorNotFound, "dialect %q does not exist", input.Name)
		}
		if requireMissing && exists {
			return operationError(ErrorAlreadyExists, "dialect %q already exists", input.Name)
		}
		dialect, prepareErr := prepareDialect(cfg, input, existing, exists)
		if prepareErr != nil {
			return prepareErr
		}
		if exists {
			if stopErr := service.stopRuntime(input.Name, existing); stopErr != nil {
				return fmt.Errorf("stop existing dialect %q: %w", input.Name, stopErr)
			}
		}
		cfg.Dialects[input.Name] = dialect
		if saveErr := saveConfig(cfg); saveErr != nil {
			return saveErr
		}
		revision, revisionErr := configRevision(cfg)
		if revisionErr != nil {
			return revisionErr
		}
		result = DialectMutationResult{Dialect: safeDialectView(input.Name, dialect), Created: !exists, Revision: revision}
		return nil
	})
	return result, err
}

func validEnvironmentVariableName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '_' {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func validateCustomUpstream(dialect Dialect) error {
	if dialect.AuthTokenEnv != "" && !validEnvironmentVariableName(dialect.AuthTokenEnv) {
		return operationError(ErrorInvalidInput, "custom upstream token environment variable name is invalid")
	}
	if dialect.BaseURL == "" && dialect.AuthTokenEnv == "" {
		return nil
	}
	if dialect.BaseURL == "" && dialect.Bridge != "" {
		return nil
	}
	if dialect.BaseURL == "" || dialect.AuthTokenEnv == "" {
		return operationError(ErrorInvalidInput, "custom upstream requires both --base-url and --auth-token-env")
	}
	endpoint, err := url.Parse(dialect.BaseURL)
	if err != nil || !endpoint.IsAbs() || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil {
		return operationError(ErrorInvalidInput, "custom upstream base URL must be an absolute http or https URL without user information")
	}
	return nil
}

func prepareDialect(cfg *Config, input DialectInput, existing Dialect, exists bool) (Dialect, error) {
	var dialect Dialect
	if input.Preset != "" {
		var ok bool
		dialect, ok = presets[input.Preset]
		if !ok {
			return Dialect{}, operationError(ErrorInvalidInput, "unknown preset %q (available: %s)", input.Preset, strings.Join(presetNames(), ", "))
		}
		dialect.Preset = input.Preset
	}
	if input.Model != "" {
		dialect.Model = input.Model
	}
	if dialect.Model == "" {
		return Dialect{}, operationError(ErrorInvalidInput, "choose --preset or provide --model")
	}
	if input.SubagentModel != "" {
		dialect.SubagentModel = input.SubagentModel
	}
	if dialect.SubagentModel == "" {
		dialect.SubagentModel = dialect.Model
	}
	if input.OpusModel != "" {
		dialect.OpusModel = input.OpusModel
	}
	if input.SonnetModel != "" {
		dialect.SonnetModel = input.SonnetModel
	}
	if input.HaikuModel != "" {
		dialect.HaikuModel = input.HaikuModel
	}
	if dialect.OpusModel == "" {
		dialect.OpusModel = dialect.Model
	}
	if dialect.SonnetModel == "" {
		dialect.SonnetModel = dialect.Model
	}
	if dialect.HaikuModel == "" {
		dialect.HaikuModel = dialect.Model
	}
	if input.EffortLevel != "" {
		dialect.EffortLevel = input.EffortLevel
	}
	if dialect.EffortLevel == "" {
		dialect.EffortLevel = "auto"
	}
	if !validEffort(dialect.EffortLevel) {
		return Dialect{}, operationError(ErrorInvalidInput, "invalid effort level %q", dialect.EffortLevel)
	}
	if input.Concurrency != 0 {
		dialect.Concurrency = input.Concurrency
	}
	if dialect.Concurrency == 0 {
		dialect.Concurrency = 3
	}
	if input.BaseURL != "" {
		dialect.BaseURL = input.BaseURL
	}
	if input.AuthTokenEnv != "" {
		dialect.AuthTokenEnv = input.AuthTokenEnv
	}
	if err := validateCustomUpstream(dialect); err != nil {
		return Dialect{}, err
	}
	dialect.Effort = input.Effort
	dialect.ToolSearch = input.ToolSearch
	if exists {
		dialect.Port = existing.Port
		dialect.APIKey = existing.APIKey
		if dialect.Bridge != "" && existing.Bridge == dialect.Bridge {
			dialect.BridgePort = existing.BridgePort
		}
	} else {
		dialect.Port = nextPort(cfg)
		if dialect.Port == 0 {
			return Dialect{}, errors.New("no proxy ports available")
		}
		var err error
		dialect.APIKey, err = newAPIKey()
		if err != nil {
			return Dialect{}, err
		}
	}
	if input.Port != 0 {
		if input.Port < 1024 || input.Port > 65535 {
			return Dialect{}, operationError(ErrorInvalidInput, "--port must be between 1024 and 65535")
		}
		for otherName, other := range cfg.Dialects {
			if otherName != input.Name && (other.Port == input.Port || other.BridgePort == input.Port) {
				return Dialect{}, operationError(ErrorInvalidInput, "port %d is already reserved by %q", input.Port, otherName)
			}
		}
		if !exists || existing.Port != input.Port {
			if !portAvailable(input.Port) {
				return Dialect{}, operationError(ErrorInvalidInput, "port %d is already in use by another process", input.Port)
			}
		}
		dialect.Port = input.Port
	}
	if dialect.Bridge != "" && dialect.BridgePort == 0 {
		dialect.BridgePort = nextAvailablePort(cfg, map[int]bool{dialect.Port: true})
		if dialect.BridgePort == 0 {
			return Dialect{}, errors.New("no provider bridge ports available")
		}
	}
	if input.BridgePort != 0 {
		if dialect.Bridge == "" {
			return Dialect{}, operationError(ErrorInvalidInput, "--bridge-port requires a preset with a managed provider bridge")
		}
		if input.BridgePort < 1024 || input.BridgePort > 65535 {
			return Dialect{}, operationError(ErrorInvalidInput, "--bridge-port must be between 1024 and 65535")
		}
		if input.BridgePort == dialect.Port {
			return Dialect{}, operationError(ErrorInvalidInput, "--bridge-port must differ from --port")
		}
		for otherName, other := range cfg.Dialects {
			if otherName != input.Name && (other.Port == input.BridgePort || other.BridgePort == input.BridgePort) {
				return Dialect{}, operationError(ErrorInvalidInput, "bridge port %d is already reserved by %q", input.BridgePort, otherName)
			}
		}
		if !exists || existing.BridgePort != input.BridgePort {
			if !portAvailable(input.BridgePort) {
				return Dialect{}, operationError(ErrorInvalidInput, "bridge port %d is already in use by another process", input.BridgePort)
			}
		}
		dialect.BridgePort = input.BridgePort
	}
	if dialect.BridgePort == dialect.Port {
		return Dialect{}, operationError(ErrorInvalidInput, "proxy and provider bridge cannot share port %d", dialect.Port)
	}
	return dialect, nil
}

func requireConfigRevision(cfg *Config, expected string) error {
	if expected == "" {
		return nil
	}
	actual, err := configRevision(cfg)
	if err != nil {
		return err
	}
	if actual != expected {
		return operationError(ErrorRevisionConflict, "configuration changed; reload and try again")
	}
	return nil
}

func (service *appService) RemoveDialect(name, expectedRevision string) error {
	if !validName(name) {
		return operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	return withStateLock(func() error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if err = requireConfigRevision(cfg, expectedRevision); err != nil {
			return err
		}
		dialect, ok := cfg.Dialects[name]
		if !ok {
			return operationError(ErrorNotFound, "dialect %q does not exist", name)
		}
		if err = service.stopRuntime(name, dialect); err != nil {
			return fmt.Errorf("stop dialect %q: %w", name, err)
		}
		delete(cfg.Dialects, name)
		if err = saveConfig(cfg); err != nil {
			return err
		}
		home, _, _, _, _, _, _, err := paths(name)
		if err != nil {
			return err
		}
		return os.RemoveAll(filepath.Join(home, "instances", name))
	})
}

func (service *appService) StartDialect(name string) (RuntimeStatus, error) {
	var status RuntimeStatus
	err := service.withDialectMutation(name, func(dialect Dialect) error {
		if missing := missingAuthProviders(name, dialect); len(missing) > 0 {
			return notAuthenticatedError(name, missing)
		}
		if err := service.startRuntime(name, dialect); err != nil {
			return err
		}
		status = service.runtimeStatus(name, dialect)
		return nil
	})
	return status, err
}

func (service *appService) StopDialect(name string) (RuntimeStatus, error) {
	var status RuntimeStatus
	err := service.withDialectMutation(name, func(dialect Dialect) error {
		if err := service.stopRuntime(name, dialect); err != nil {
			return err
		}
		status = service.runtimeStatus(name, dialect)
		return nil
	})
	return status, err
}

func (service *appService) RestartDialect(name string) (RuntimeStatus, error) {
	var status RuntimeStatus
	err := service.withDialectMutation(name, func(dialect Dialect) error {
		if missing := missingAuthProviders(name, dialect); len(missing) > 0 {
			return notAuthenticatedError(name, missing)
		}
		if err := service.stopRuntime(name, dialect); err != nil {
			return err
		}
		if err := service.startRuntime(name, dialect); err != nil {
			return err
		}
		status = service.runtimeStatus(name, dialect)
		return nil
	})
	return status, err
}

func (service *appService) withDialectMutation(name string, operation func(Dialect) error) error {
	if !validName(name) {
		return operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	return withStateLock(func() error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dialect, ok := cfg.Dialects[name]
		if !ok {
			return operationError(ErrorNotFound, "dialect %q does not exist", name)
		}
		return operation(dialect)
	})
}

func (service *appService) DialectStatus(name string) (RuntimeStatus, error) {
	if !validName(name) {
		return RuntimeStatus{}, operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	cfg, err := loadConfig()
	if err != nil {
		return RuntimeStatus{}, err
	}
	dialect, ok := cfg.Dialects[name]
	if !ok {
		return RuntimeStatus{}, operationError(ErrorNotFound, "dialect %q does not exist", name)
	}
	return service.runtimeStatus(name, dialect), nil
}

func (service *appService) runtimeStatus(name string, dialect Dialect) RuntimeStatus {
	proxyRunning := service.proxyProbe(dialect)
	status := RuntimeStatus{Proxy: ComponentStatus{State: RuntimeStopped, Port: dialect.Port}}
	if proxyRunning {
		status.Proxy.State = RuntimeRunning
		status.Proxy.PID = proxyPID(name)
	}
	if dialect.Bridge == "" {
		status.State = status.Proxy.State
		return status
	}
	bridgeRunning := service.bridgeProbe(dialect)
	bridge := &ComponentStatus{State: RuntimeStopped, Port: dialect.BridgePort}
	if bridgeRunning {
		bridge.State = RuntimeRunning
		bridge.PID = managedBridgePID(name, dialect)
	}
	status.Bridge = bridge
	switch {
	case proxyRunning && bridgeRunning:
		status.State = RuntimeRunning
	case !proxyRunning && !bridgeRunning:
		status.State = RuntimeStopped
	default:
		status.State = RuntimeDegraded
	}
	return status
}

func (service *appService) CursorStatus() CursorRuntimeStatus {
	return service.cursorStatusFn()
}

func (service *appService) InstallCursorRuntime() (result CursorInstallResult, err error) {
	err = withStateLock(func() error {
		var installErr error
		result, installErr = service.cursorInstallFn()
		return installErr
	})
	return result, err
}

func (service *appService) ListNativeLaunchers() ([]NativeLauncherView, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", err
	}
	names := make([]string, 0, len(cfg.NativeLaunchers))
	for name := range cfg.NativeLaunchers {
		names = append(names, name)
	}
	sort.Strings(names)
	launchers := make([]NativeLauncherView, 0, len(names))
	for _, name := range names {
		record := cfg.NativeLaunchers[name]
		launchers = append(launchers, nativeLauncherView(name, record))
	}
	revision, err := configRevision(cfg)
	return launchers, revision, err
}

func (service *appService) InstallNativeLauncher(input NativeLauncherInput, expectedRevision string) (result NativeLauncherResult, err error) {
	if !validName(input.Name) || input.Name == "claude" {
		return result, operationError(ErrorInvalidInput, "native launcher requires a valid command name other than claude")
	}
	if input.Directory != "" && !filepath.IsAbs(input.Directory) {
		return result, operationError(ErrorInvalidInput, "native launcher directory must be an absolute path or left blank")
	}
	err = withStateLock(func() error {
		cfg, loadErr := loadConfig()
		if loadErr != nil {
			return loadErr
		}
		if revisionErr := requireConfigRevision(cfg, expectedRevision); revisionErr != nil {
			return revisionErr
		}
		claudePath, lookErr := exec.LookPath("claude")
		if lookErr != nil {
			return errors.New("Claude Code not found in PATH")
		}
		claudePath, lookErr = filepath.Abs(claudePath)
		if lookErr != nil {
			return lookErr
		}
		directory := input.Directory
		if directory == "" {
			home, _ := os.UserHomeDir()
			directory = filepath.Join(home, ".local", "bin")
		}
		path, lookErr := filepath.Abs(filepath.Join(directory, input.Name))
		if lookErr != nil {
			return lookErr
		}
		if alias, found := zshAlias(input.Name); found {
			return fmt.Errorf("zsh alias %q would override the installed command; remove it from ~/.zshrc and run `unalias %s` in already-open terminals", alias, input.Name)
		}
		if conflicts := commandConflicts(input.Name, path); len(conflicts) > 0 {
			return fmt.Errorf("command %q already exists at %s; remove it or choose another launcher name", input.Name, strings.Join(conflicts, ", "))
		}
		body := []byte(nativeLauncherBody(claudePath, input.Dangerous))
		newHash := contentSHA256(body)
		existing, tracked := cfg.NativeLaunchers[input.Name]
		var previous []byte
		targetExisted := false
		if tracked {
			if canonicalPath(existing.Path) != canonicalPath(path) {
				return operationError(ErrorInvalidInput, "tracked native launcher path cannot be changed; remove %q before reinstalling it in another directory", input.Name)
			}
			var verifyErr error
			previous, verifyErr = readVerifiedNativeLauncher(existing)
			if verifyErr != nil {
				return verifyErr
			}
			targetExisted = true
		} else if current, readErr := os.ReadFile(path); readErr == nil {
			if contentSHA256(current) != newHash {
				return operationError(ErrorExternallyModified, "launcher path %s already exists and was not created by cc-dialect", path)
			}
			previous = current
			targetExisted = true
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if lookErr = os.MkdirAll(directory, 0o755); lookErr != nil {
			return lookErr
		}
		var committedLauncherWriteErr error
		if writeErr := atomicWriteFile(path, body, 0o755); writeErr != nil {
			if !atomicWriteCommitted(writeErr) {
				return writeErr
			}
			committedLauncherWriteErr = writeErr
		}
		record := NativeLauncher{Path: path, ClaudePath: claudePath, Dangerous: input.Dangerous, SHA256: newHash}
		cfg.NativeLaunchers[input.Name] = record
		if saveErr := saveConfig(cfg); saveErr != nil {
			if atomicWriteCommitted(saveErr) {
				return saveErr
			}
			var rollbackErr error
			if targetExisted {
				rollbackErr = atomicWriteFile(path, previous, 0o755)
			} else {
				rollbackErr = os.Remove(path)
			}
			return errors.Join(saveErr, rollbackErr)
		}
		revision, revisionErr := configRevision(cfg)
		if revisionErr != nil {
			return revisionErr
		}
		result = NativeLauncherResult{Launcher: nativeLauncherView(input.Name, record), Revision: revision}
		return committedLauncherWriteErr
	})
	return result, err
}

func (service *appService) RemoveNativeLauncher(name, expectedRevision string) error {
	if !validName(name) || name == "claude" {
		return operationError(ErrorInvalidInput, "invalid native launcher name %q", name)
	}
	return withStateLock(func() error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if err = requireConfigRevision(cfg, expectedRevision); err != nil {
			return err
		}
		record, ok := cfg.NativeLaunchers[name]
		if !ok {
			return operationError(ErrorNotFound, "native launcher %q does not exist", name)
		}
		body, err := readVerifiedNativeLauncher(record)
		if err != nil {
			return err
		}
		if err = os.Remove(record.Path); err != nil {
			return err
		}
		delete(cfg.NativeLaunchers, name)
		if err = saveConfig(cfg); err != nil {
			if atomicWriteCommitted(err) {
				return err
			}
			return errors.Join(err, atomicWriteFile(record.Path, body, 0o755))
		}
		return nil
	})
}

func nativeLauncherView(name string, record NativeLauncher) NativeLauncherView {
	return NativeLauncherView{
		Name: name, Path: record.Path, ClaudePath: record.ClaudePath,
		Dangerous: record.Dangerous, Verified: verifyNativeLauncher(record) == nil,
	}
}

func verifyNativeLauncher(record NativeLauncher) error {
	_, err := readVerifiedNativeLauncher(record)
	return err
}

func readVerifiedNativeLauncher(record NativeLauncher) ([]byte, error) {
	data, err := os.ReadFile(record.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, operationError(ErrorExternallyModified, "tracked native launcher %s is missing", record.Path)
		}
		return nil, err
	}
	if contentSHA256(data) != record.SHA256 {
		return nil, operationError(ErrorExternallyModified, "tracked native launcher %s was modified outside cc-dialect", record.Path)
	}
	return data, nil
}

func contentSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
