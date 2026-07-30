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
	cursorSDKVersion   = "1.0.26"
	cursorMinNodeMajor = 22
	cursorMinNodeMinor = 13
	// cursorAgentStoreDir is the SDK's local agent store, written by the bridge
	// inside the workspace it is handed. The bridge scopes one store per Claude
	// Code turn (reused across tool-call steps) and discards it when the turn
	// ends or goes idle; this is the parent every launch starts from empty,
	// which is also what repairs an instance whose store grew under an older
	// build.
	cursorAgentStoreDir = "cursor-workspace/.cursor-dialect-state"
	// cursorBridgeHeapMB pins the bridge's V8 old-space ceiling. Node derives its
	// default from the machine's memory, so the size a bridge dies at otherwise
	// depends on the host — a smaller machine crashes on a parse a larger one
	// completes. This is a mitigation for a single oversized payload, not the
	// bound on growth: that is the per-turn store.
	cursorBridgeHeapMB = 4096
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
		if stopProxyDialectByName(name, cfg.Dialects[name]) == nil {
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
	return instance.runningPID("cursor-bridge.pid")
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

// resetCursorAgentStore empties the SDK's local agent store before a bridge is
// launched.
//
// The SDK appends every checkpoint and run event to the store and reads the file
// back whole, so a store shared across requests grows monotonically until a
// parse of it exhausts the bridge's heap and kills the process mid-session. The
// bridge now gives each request its own store underneath this directory, but a
// bridge that died leaves its run's directory behind, and an instance created
// under an older build still holds everything it ever wrote. Clearing the parent
// at launch reclaims both.
//
// This runs only once no bridge holds the port, so it cannot truncate state a
// concurrent run is appending to.
func resetCursorAgentStore(instance *instanceFS) error {
	if err := instance.RemoveTree(cursorAgentStoreDir); err != nil {
		return fmt.Errorf("clear the Cursor agent store for %q: %w", instance.name, err)
	}
	return nil
}

// cursorBridgeNodeArgs builds the node argument vector. The V8 options come
// first: everything after the script path is passed to the script instead.
func cursorBridgeNodeArgs(bridgePath string, port int, workspace string) []string {
	return []string{
		fmt.Sprintf("--max-old-space-size=%d", cursorBridgeHeapMB),
		bridgePath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--workspace", workspace,
	}
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

// startCursorBridge returns a closure that tears down the bridge it launched, or
// nil when it launched nothing. A caller whose own startup fails afterwards uses
// it to undo exactly what this call did. The closure carries the child's process
// handle rather than deferring to stopCursorBridge, which removes the PID without
// signalling whenever its health probe fails — for a bridge that is alive but
// wedged that deletes the only record of it and strands the process.
//
// An already-serving bridge is adopted only when the pinned directory holds a
// live record of owning it. Health is answered by the port, and the port cannot
// say which directory the process belongs to: a bridge left over from a
// directory that has since been replaced answers just as readily, and adopting
// it would record nothing under the pinned root for a later stop to find.
func startCursorBridge(instance *instanceFS, dialect Dialect) (func() error, error) {
	if dialect.Bridge != "cursor" {
		return nil, nil
	}
	if cursorBridgeHealthy(dialect) {
		if pid := instance.runningPID("cursor-bridge.pid"); pid > 0 && processAlive(pid) {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"a Cursor bridge is already serving port %d but dialect %q holds no record of owning it; stop it and start again",
			dialect.BridgePort, instance.name)
	}
	return launchCursorBridge(instance, dialect)
}

func launchCursorBridge(instance *instanceFS, dialect Dialect) (func() error, error) {
	if os.Getenv("CURSOR_API_KEY") == "" {
		return nil, errors.New("CURSOR_API_KEY is not set")
	}
	name := instance.name
	nodePath, _, err := cursorNode()
	if err != nil {
		return nil, err
	}
	runtimeDir, bridgePath, _, err := cursorRuntimePaths()
	if err != nil {
		return nil, err
	}
	if err = requireCursorRuntime(); err != nil {
		return nil, err
	}
	if err = writeCursorBridge(bridgePath); err != nil {
		return nil, err
	}
	workspace, logFile, err := prepareCursorBridgeFiles(instance)
	if err != nil {
		return nil, err
	}
	// The child dups the descriptor at Start, so holding the parent's copy
	// until return costs one fd and removes a close from every error path.
	defer func() { _ = logFile.Close() }()
	logPath, err := instance.Abs("cursor-bridge.log")
	if err != nil {
		return nil, err
	}
	if pid := instance.runningPID("cursor-bridge.pid"); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.BridgePort) {
			return nil, fmt.Errorf("Cursor bridge process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`",
				pid, dialect.BridgePort, name)
		}
		_ = instance.RemoveIfExists("cursor-bridge.pid")
	}
	if !portAvailable(dialect.BridgePort) {
		return nil, fmt.Errorf("bridge port %d for %q is already in use by another process", dialect.BridgePort, name)
	}
	// After the port and PID checks above, so a bridge that is alive but wedged
	// keeps the store its in-flight runs are still writing to.
	if err = resetCursorAgentStore(instance); err != nil {
		return nil, err
	}
	// The Cursor SDK accepts the workspace only as an absolute pathname. Rooted
	// preparation above rejects a pre-existing escape; later SDK pathname I/O is
	// an external subprocess trust boundary.
	cmd := exec.Command(nodePath, cursorBridgeNodeArgs(bridgePath, dialect.BridgePort, workspace)...)
	cmd.Dir = runtimeDir
	cmd.Env = cursorBridgeEnvironment(dialect.APIKey)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	exited := monitorStartedProcess(cmd)
	// Bound to this child, so unwinding kills the process itself rather than
	// asking the health endpoint whether it deserves to be signalled. A PID this
	// call just started cannot be a stale or reused one, which is the only reason
	// the shared stop path withholds the signal.
	unwind := func() error { return cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid") }
	if err = instance.WritePID("cursor-bridge.pid", cmd.Process.Pid); err != nil {
		cleanupErr := cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid")
		return nil, errors.Join(fmt.Errorf("record Cursor bridge PID: %w", err), cleanupErr)
	}
	// Best-effort, unlike the PID: the bridge is already started and serving, and
	// a missing version marker only makes doctor report an unknown build and
	// prompt a restart. Tearing down a working bridge over it would be worse than
	// the stale marker it avoids.
	_ = instance.WriteBuildIdentity("cursor-bridge.version")
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		select {
		case waitErr := <-exited:
			cleanupErr := instance.RemoveIfExists("cursor-bridge.pid")
			if waitErr == nil {
				return nil, errors.Join(fmt.Errorf("Cursor bridge exited during startup; see %s", logPath), cleanupErr)
			}
			return nil, errors.Join(fmt.Errorf("Cursor bridge exited during startup: %w; see %s", waitErr, logPath), cleanupErr)
		default:
		}
		if cursorBridgeHealthy(dialect) {
			return unwind, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	cleanupErr := cleanupStartedProcess(cmd, exited, instance, "cursor-bridge.pid")
	return nil, errors.Join(fmt.Errorf("timed out starting Cursor bridge; see %s", logPath), cleanupErr)
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

// stopCursorBridge takes the caller's pinned instance for the same reason
// startCursorBridge does: opening a second handle would resolve the dialect name
// again, and a directory replaced in between would hide the running bridge's PID
// behind the replacement's missing one.
func stopCursorBridge(instance *instanceFS, dialect Dialect) error {
	if dialect.Bridge != "cursor" {
		return nil
	}
	name := instance.name
	pid, pidErr := instance.ReadPID("cursor-bridge.pid")
	if pidErr != nil {
		// An unreadable ownership record must not read as "already stopped": that
		// would abandon a live bridge with nothing left pointing at it. The port is
		// the liveness signal — a wedged or still-starting bridge fails a health
		// probe while alive, but still holds its port.
		if portBusy(dialect.BridgePort) {
			return fmt.Errorf("Cursor bridge for %q still holds port %d but its PID record cannot be read safely: %w", name, dialect.BridgePort, pidErr)
		}
		return nil
	}
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
