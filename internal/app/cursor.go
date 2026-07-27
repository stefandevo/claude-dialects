package app

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cursorSDKVersion   = "1.0.23"
	cursorMinNodeMajor = 22
	cursorMinNodeMinor = 13
)

//go:embed cursor_bridge.mjs
var cursorBridgeSource []byte

func cursorCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cc-dialect cursor <install|status|models>")
	}
	service := newAppService()
	switch args[0] {
	case "install":
		if _, version, nodeErr := cursorNode(); nodeErr == nil {
			if _, npmErr := exec.LookPath("npm"); npmErr == nil {
				fmt.Printf("Installing official @cursor/sdk %s with Node %s…\n", cursorSDKVersion, version)
			}
		}
		result, err := service.InstallCursorRuntime()
		if err != nil {
			return err
		}
		fmt.Printf("Cursor bridge ready (@cursor/sdk %s, %s)\n", result.InstalledVersion, result.NodePath)
		if len(result.StoppedDialects) > 0 {
			fmt.Printf("Stopped stale Cursor dialect runtime(s): %s\n", strings.Join(result.StoppedDialects, ", "))
			fmt.Println("Launch the dialect again to use the updated bridge.")
		}
		fmt.Println("Set CURSOR_API_KEY, then create a dialect with --preset cursor-composer.")
		return nil
	case "status":
		status := service.CursorStatus()
		if status.NodeError != "" {
			fmt.Println("✗", status.NodeError)
		} else {
			fmt.Printf("✓ Node %s: %s\n", status.NodeVersion, status.NodePath)
		}
		if !status.RuntimeInstalled {
			fmt.Println("✗ Cursor bridge runtime is not installed (run: cc-dialect cursor install)")
		} else if !status.RuntimeCurrent {
			fmt.Printf("✗ @cursor/sdk %s is installed; %s is required (run: cc-dialect cursor install)\n",
				status.InstalledVersion, status.RequiredVersion)
		} else {
			fmt.Printf("✓ @cursor/sdk %s\n", status.InstalledVersion)
		}
		if !status.APIKeySet {
			fmt.Println("✗ CURSOR_API_KEY is not set")
		} else {
			fmt.Println("✓ CURSOR_API_KEY")
		}
		if status.NodeError != "" {
			return errors.New(status.NodeError)
		}
		return nil
	case "models":
		models, err := cursorSDKModels()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(models, "\n"))
		return nil
	default:
		return fmt.Errorf("unknown cursor command %q", args[0])
	}
}

func cursorRuntimePaths() (runtimeDir, bridgePath, packagePath string, err error) {
	home, err := homeDir()
	if err != nil {
		return "", "", "", err
	}
	runtimeDir = filepath.Join(home, "cursor-runtime")
	bridgePath = filepath.Join(runtimeDir, "cursor_bridge.mjs")
	packagePath = filepath.Join(runtimeDir, "node_modules", "@cursor", "sdk", "package.json")
	return
}

type CursorRuntimeStatus struct {
	NodePath         string `json:"nodePath,omitempty"`
	NodeVersion      string `json:"nodeVersion,omitempty"`
	NodeError        string `json:"nodeError,omitempty"`
	RuntimeInstalled bool   `json:"runtimeInstalled"`
	RuntimeCurrent   bool   `json:"runtimeCurrent"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	RequiredVersion  string `json:"requiredVersion"`
	APIKeySet        bool   `json:"apiKeySet"`
}

type CursorInstallResult struct {
	NodePath         string   `json:"nodePath"`
	NodeVersion      string   `json:"nodeVersion"`
	InstalledVersion string   `json:"installedVersion"`
	StoppedDialects  []string `json:"stoppedDialects"`
}

func installCursorRuntime() (CursorInstallResult, error) {
	nodePath, version, err := cursorNode()
	if err != nil {
		return CursorInstallResult{}, err
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return CursorInstallResult{}, errors.New("npm was not found in PATH; install Node.js 22.13 or newer first")
	}
	runtimeDir, bridgePath, _, err := cursorRuntimePaths()
	if err != nil {
		return CursorInstallResult{}, err
	}
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		return CursorInstallResult{}, err
	}
	if err = os.Chmod(runtimeDir, 0o700); err != nil {
		return CursorInstallResult{}, err
	}
	if err = writeCursorBridge(bridgePath); err != nil {
		return CursorInstallResult{}, err
	}
	packageJSON := fmt.Sprintf("{\n  \"private\": true,\n  \"type\": \"module\",\n  \"dependencies\": {\n    \"@cursor/sdk\": %q\n  }\n}\n", cursorSDKVersion)
	if err = atomicWriteFile(filepath.Join(runtimeDir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		return CursorInstallResult{}, err
	}
	cmd := exec.Command(npmPath, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--omit=dev")
	cmd.Dir = runtimeDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		return CursorInstallResult{}, fmt.Errorf("install @cursor/sdk: %w", err)
	}
	installed, err := cursorRuntimeVersion()
	if err != nil {
		return CursorInstallResult{}, err
	}
	return CursorInstallResult{
		NodePath: nodePath, NodeVersion: version, InstalledVersion: installed,
		StoppedDialects: stopRunningCursorDialects(),
	}, nil
}

func stopRunningCursorDialects() []string {
	cfg, err := loadConfig()
	if err != nil {
		return []string{}
	}
	names := make([]string, 0)
	for name, dialect := range cfg.Dialects {
		if dialect.Bridge == "cursor" && (proxyHealthy(dialect) || managedBridgeHealthy(dialect)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	stopped := make([]string, 0, len(names))
	for _, name := range names {
		if stopProxyDialect(name, cfg.Dialects[name]) == nil {
			stopped = append(stopped, name)
		}
	}
	return stopped
}

func inspectCursorRuntime() CursorRuntimeStatus {
	status := CursorRuntimeStatus{
		RequiredVersion: cursorSDKVersion,
		APIKeySet:       os.Getenv("CURSOR_API_KEY") != "",
	}
	nodePath, nodeVersion, nodeErr := cursorNode()
	status.NodePath, status.NodeVersion = nodePath, nodeVersion
	if nodeErr != nil {
		status.NodeError = nodeErr.Error()
	}
	if version, err := cursorRuntimeVersion(); err == nil {
		status.RuntimeInstalled = true
		status.InstalledVersion = version
		status.RuntimeCurrent = version == cursorSDKVersion
	}
	return status
}

func cursorNode() (path, version string, err error) {
	path, err = exec.LookPath("node")
	if err != nil {
		return "", "", errors.New("Node.js was not found in PATH; Cursor support requires Node.js 22.13 or newer")
	}
	raw, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("read Node.js version: %w", err)
	}
	version = strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
	major, minor, ok := parseMajorMinor(version)
	if !ok || major < cursorMinNodeMajor || major == cursorMinNodeMajor && minor < cursorMinNodeMinor {
		return "", "", fmt.Errorf("Node.js %s is unsupported; Cursor support requires Node.js 22.13 or newer", version)
	}
	return path, version, nil
}

func parseMajorMinor(version string) (major, minor int, ok bool) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	return major, minor, majorErr == nil && minorErr == nil
}

func cursorRuntimeVersion() (string, error) {
	_, _, packagePath, err := cursorRuntimePaths()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(packagePath)
	if err != nil {
		return "", err
	}
	var metadata struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return "", err
	}
	if metadata.Version == "" {
		return "", errors.New("installed @cursor/sdk package has no version")
	}
	return metadata.Version, nil
}

func writeCursorBridge(path string) error {
	return atomicWriteFile(path, cursorBridgeSource, 0o600)
}

func cursorSDKModels() ([]string, error) {
	if os.Getenv("CURSOR_API_KEY") == "" {
		return nil, errors.New("CURSOR_API_KEY is not set")
	}
	nodePath, _, err := cursorNode()
	if err != nil {
		return nil, err
	}
	runtimeDir, _, _, err := cursorRuntimePaths()
	if err != nil {
		return nil, err
	}
	if err = requireCursorRuntime(); err != nil {
		return nil, err
	}
	script := `import { Cursor } from "@cursor/sdk";
const response = await Cursor.models.list({ apiKey: process.env.CURSOR_API_KEY });
const items = Array.isArray(response) ? response : (response?.items || response?.models || []);
process.stdout.write(JSON.stringify(items.map((item) => item.id).filter(Boolean)));`
	cmd := exec.Command(nodePath, "--input-type=module", "--eval", script)
	cmd.Dir = runtimeDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list Cursor models: %s", strings.TrimSpace(string(output)))
	}
	var models []string
	if err = json.Unmarshal(output, &models); err != nil {
		return nil, fmt.Errorf("decode Cursor models: %w", err)
	}
	sort.Strings(models)
	return models, nil
}

func cursorBridgePID(name string) int {
	instance, err := openInstanceFS(name)
	if err != nil {
		return 0
	}
	defer instance.Close()
	return instance.ReadPID("cursor-bridge.pid")
}

func prepareCursorBridgeFiles(instance *instanceFS) (string, *os.File, error) {
	if err := instance.MkdirAll("cursor-workspace", 0o700); err != nil {
		return "", nil, err
	}
	if err := instance.Chmod("cursor-workspace", 0o700); err != nil {
		return "", nil, err
	}
	workspace, err := instance.Abs("cursor-workspace")
	if err != nil {
		return "", nil, err
	}
	logFile, err := instance.OpenAppend("cursor-bridge.log", 0o600)
	if err != nil {
		return "", nil, err
	}
	return workspace, logFile, nil
}

func cursorBridgeHealthy(dialect Dialect) bool {
	if dialect.Bridge != "cursor" || dialect.BridgePort == 0 {
		return false
	}
	client := &http.Client{Timeout: 700 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", dialect.BridgePort), nil)
	req.Header.Set("Authorization", "Bearer "+dialect.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func startCursorBridge(name string, dialect Dialect) error {
	if dialect.Bridge != "cursor" {
		return nil
	}
	if cursorBridgeHealthy(dialect) {
		return nil
	}
	if os.Getenv("CURSOR_API_KEY") == "" {
		return errors.New("CURSOR_API_KEY is not set")
	}
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	nodePath, _, err := cursorNode()
	if err != nil {
		return err
	}
	runtimeDir, bridgePath, _, err := cursorRuntimePaths()
	if err != nil {
		return err
	}
	if err = requireCursorRuntime(); err != nil {
		return err
	}
	if err = writeCursorBridge(bridgePath); err != nil {
		return err
	}
	workspace, logFile, err := prepareCursorBridgeFiles(instance)
	if err != nil {
		return err
	}
	logPath, err := instance.Abs("cursor-bridge.log")
	if err != nil {
		_ = logFile.Close()
		return err
	}
	if pid := instance.ReadPID("cursor-bridge.pid"); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.BridgePort) {
			_ = logFile.Close()
			return fmt.Errorf("Cursor bridge process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`",
				pid, dialect.BridgePort, name)
		}
		_ = instance.RemoveIfExists("cursor-bridge.pid")
	}
	if !portAvailable(dialect.BridgePort) {
		_ = logFile.Close()
		return fmt.Errorf("bridge port %d for %q is already in use by another process", dialect.BridgePort, name)
	}
	// The Cursor SDK accepts the workspace only as an absolute pathname. Rooted
	// preparation above rejects a pre-existing escape; later SDK pathname I/O is
	// an external subprocess trust boundary.
	cmd := exec.Command(nodePath, bridgePath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(dialect.BridgePort),
		"--workspace", workspace,
	)
	cmd.Dir = runtimeDir
	cmd.Env = cursorBridgeEnvironment(dialect.APIKey)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	exited := monitorStartedProcess(cmd)
	if err = instance.WritePID("cursor-bridge.pid", cmd.Process.Pid); err != nil {
		cleanupErr := cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid")
		return errors.Join(fmt.Errorf("record Cursor bridge PID: %w", err), cleanupErr)
	}
	if err = instance.WriteBuildIdentity("cursor-bridge.version"); err != nil {
		cleanupErr := cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid")
		return errors.Join(fmt.Errorf("record Cursor bridge build identity: %w", err), cleanupErr)
	}
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		select {
		case waitErr := <-exited:
			cleanupErr := instance.RemoveIfExists("cursor-bridge.pid")
			if waitErr == nil {
				return errors.Join(fmt.Errorf("Cursor bridge exited during startup; see %s", logPath), cleanupErr)
			}
			return errors.Join(fmt.Errorf("Cursor bridge exited during startup: %w; see %s", waitErr, logPath), cleanupErr)
		default:
		}
		if cursorBridgeHealthy(dialect) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	cleanupErr := cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid")
	return errors.Join(fmt.Errorf("timed out starting Cursor bridge; see %s", logPath), cleanupErr)
}

func requireCursorRuntime() error {
	version, err := cursorRuntimeVersion()
	if err != nil {
		return errors.New("Cursor bridge runtime is not installed; run: cc-dialect cursor install")
	}
	if version != cursorSDKVersion {
		return fmt.Errorf("Cursor bridge has @cursor/sdk %s but this cc-dialect requires %s; run: cc-dialect cursor install",
			version, cursorSDKVersion)
	}
	return nil
}

func cursorBridgeEnvironment(bridgeKey string) []string {
	allowed := []string{
		"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SHELL", "USER", "LOGNAME",
		"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR",
	}
	env := make([]string, 0, len(allowed)+2)
	for _, key := range allowed {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "CURSOR_API_KEY="+os.Getenv("CURSOR_API_KEY"))
	env = append(env, "CURSOR_DIALECT_BRIDGE_KEY="+bridgeKey)
	return env
}

func stopCursorBridge(name string, dialect Dialect) error {
	if dialect.Bridge != "cursor" {
		return nil
	}
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	pid := instance.ReadPID("cursor-bridge.pid")
	if pid == 0 {
		return nil
	}
	if !cursorBridgeHealthy(dialect) {
		// Never signal a stale or reused PID unless the process proves ownership
		// of this dialect's private bridge key.
		return instance.RemoveIfExists("cursor-bridge.pid")
	}
	process, err := os.FindProcess(pid)
	if err == nil && processAlive(pid) {
		_ = process.Signal(os.Interrupt)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && processAlive(pid); {
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(pid) {
			_ = process.Kill()
		}
	}
	return instance.RemoveIfExists("cursor-bridge.pid")
}

func dialectHealthy(dialect Dialect) bool {
	if !proxyHealthy(dialect) {
		return false
	}
	return managedBridgeHealthy(dialect)
}
