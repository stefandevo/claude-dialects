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
	switch args[0] {
	case "install":
		return installCursorRuntime()
	case "status":
		return cursorStatus()
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

func cursorInstancePaths(name string) (pidPath, logPath, workspace string, err error) {
	home, err := homeDir()
	if err != nil {
		return "", "", "", err
	}
	instanceDir := filepath.Join(home, "instances", name)
	return filepath.Join(instanceDir, "cursor-bridge.pid"),
		filepath.Join(instanceDir, "cursor-bridge.log"),
		filepath.Join(instanceDir, "cursor-workspace"), nil
}

func installCursorRuntime() error {
	nodePath, version, err := cursorNode()
	if err != nil {
		return err
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return errors.New("npm was not found in PATH; install Node.js 22.13 or newer first")
	}
	runtimeDir, bridgePath, _, err := cursorRuntimePaths()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	if err = os.Chmod(runtimeDir, 0o700); err != nil {
		return err
	}
	if err = writeCursorBridge(bridgePath); err != nil {
		return err
	}
	packageJSON := fmt.Sprintf("{\n  \"private\": true,\n  \"type\": \"module\",\n  \"dependencies\": {\n    \"@cursor/sdk\": %q\n  }\n}\n", cursorSDKVersion)
	if err = os.WriteFile(filepath.Join(runtimeDir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		return err
	}
	fmt.Printf("Installing official @cursor/sdk %s with Node %s…\n", cursorSDKVersion, version)
	cmd := exec.Command(npmPath, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--omit=dev")
	cmd.Dir = runtimeDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("install @cursor/sdk: %w", err)
	}
	installed, err := cursorRuntimeVersion()
	if err != nil {
		return err
	}
	fmt.Printf("Cursor bridge ready (@cursor/sdk %s, %s)\n", installed, nodePath)
	fmt.Println("Set CURSOR_API_KEY, then create a dialect with --preset cursor-composer.")
	return nil
}

func cursorStatus() error {
	nodePath, nodeVersion, nodeErr := cursorNode()
	if nodeErr != nil {
		fmt.Println("✗", nodeErr)
	} else {
		fmt.Printf("✓ Node %s: %s\n", nodeVersion, nodePath)
	}
	if version, err := cursorRuntimeVersion(); err != nil {
		fmt.Println("✗ Cursor bridge runtime is not installed (run: cc-dialect cursor install)")
	} else if version != cursorSDKVersion {
		fmt.Printf("✗ @cursor/sdk %s is installed; %s is required (run: cc-dialect cursor install)\n",
			version, cursorSDKVersion)
	} else {
		fmt.Printf("✓ @cursor/sdk %s\n", version)
	}
	if os.Getenv("CURSOR_API_KEY") == "" {
		fmt.Println("✗ CURSOR_API_KEY is not set")
	} else {
		fmt.Println("✓ CURSOR_API_KEY")
	}
	if nodeErr != nil {
		return nodeErr
	}
	return nil
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
	if err := os.WriteFile(path, cursorBridgeSource, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
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
	pidPath, _, _, err := cursorInstancePaths(name)
	if err != nil {
		return 0
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
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
	pidPath, logPath, workspace, err := cursorInstancePaths(name)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		return err
	}
	if err = os.MkdirAll(workspace, 0o700); err != nil {
		return err
	}
	if pid := cursorBridgePID(name); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.BridgePort) {
			return fmt.Errorf("Cursor bridge process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`",
				pid, dialect.BridgePort, name)
		}
		_ = os.Remove(pidPath)
	}
	if !portAvailable(dialect.BridgePort) {
		return fmt.Errorf("bridge port %d for %q is already in use by another process", dialect.BridgePort, name)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
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
	if err = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		if cursorBridgeHealthy(dialect) {
			return nil
		}
		if !processAlive(cmd.Process.Pid) {
			return fmt.Errorf("Cursor bridge exited during startup; see %s", logPath)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out starting Cursor bridge; see %s", logPath)
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
	pidPath, _, _, err := cursorInstancePaths(name)
	if err != nil {
		return err
	}
	pid := cursorBridgePID(name)
	if pid == 0 {
		return nil
	}
	if !cursorBridgeHealthy(dialect) {
		// Never signal a stale or reused PID unless the process proves ownership
		// of this dialect's private bridge key.
		_ = os.Remove(pidPath)
		return nil
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
	return os.Remove(pidPath)
}

func dialectHealthy(dialect Dialect) bool {
	if !proxyHealthy(dialect) {
		return false
	}
	return managedBridgeHealthy(dialect)
}
