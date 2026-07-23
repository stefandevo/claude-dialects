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
	copilotSDKVersion   = "1.0.7"
	copilotMinNodeMajor = 22
	copilotMinNodeMinor = 13
)

//go:embed copilot_bridge.mjs
var copilotBridgeSource []byte

func copilotCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cc-dialect copilot <install|login|status|models>")
	}
	switch args[0] {
	case "install":
		return installCopilotRuntime()
	case "login":
		return copilotLogin()
	case "status":
		return copilotStatus()
	case "models":
		models, err := copilotSDKModels()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(models, "\n"))
		return nil
	default:
		return fmt.Errorf("unknown copilot command %q", args[0])
	}
}

func copilotRuntimePaths() (runtimeDir, bridgePath, packagePath, cliPath string, err error) {
	home, err := homeDir()
	if err != nil {
		return "", "", "", "", err
	}
	runtimeDir = filepath.Join(home, "copilot-runtime")
	bridgePath = filepath.Join(runtimeDir, "copilot_bridge.mjs")
	packagePath = filepath.Join(runtimeDir, "node_modules", "@github", "copilot-sdk", "package.json")
	cliPath = filepath.Join(runtimeDir, "node_modules", ".bin", "copilot")
	return
}

func copilotInstancePaths(name string) (pidPath, logPath, stateDir, versionPath string, err error) {
	home, err := homeDir()
	if err != nil {
		return "", "", "", "", err
	}
	instanceDir := filepath.Join(home, "instances", name)
	return filepath.Join(instanceDir, "copilot-bridge.pid"),
		filepath.Join(instanceDir, "copilot-bridge.log"),
		filepath.Join(instanceDir, "copilot-home"), filepath.Join(instanceDir, "copilot-bridge.version"), nil
}

func installCopilotRuntime() error {
	nodePath, version, err := copilotNode()
	if err != nil {
		return err
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return errors.New("npm was not found in PATH; install Node.js 22.13 or newer first")
	}
	runtimeDir, bridgePath, _, _, err := copilotRuntimePaths()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	if err = os.Chmod(runtimeDir, 0o700); err != nil {
		return err
	}
	if err = writeCopilotBridge(bridgePath); err != nil {
		return err
	}
	packageJSON := fmt.Sprintf("{\n  \"private\": true,\n  \"type\": \"module\",\n  \"dependencies\": {\n    \"@github/copilot-sdk\": %q\n  }\n}\n", copilotSDKVersion)
	if err = atomicWriteFile(filepath.Join(runtimeDir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		return err
	}
	fmt.Printf("Installing official @github/copilot-sdk %s with Node %s…\n", copilotSDKVersion, version)
	cmd := exec.Command(npmPath, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--omit=dev")
	cmd.Dir = runtimeDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("install @github/copilot-sdk: %w", err)
	}
	installed, err := copilotRuntimeVersion()
	if err != nil {
		return err
	}
	fmt.Printf("Copilot bridge ready (@github/copilot-sdk %s, %s)\n", installed, nodePath)
	fmt.Println("Next: cc-dialect copilot login")
	return nil
}

func copilotLogin() error {
	if err := requireCopilotRuntime(); err != nil {
		return err
	}
	_, _, _, cliPath, err := copilotRuntimePaths()
	if err != nil {
		return err
	}
	cmd := exec.Command(cliPath, "login")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func copilotStatus() error {
	nodePath, nodeVersion, nodeErr := copilotNode()
	if nodeErr != nil {
		fmt.Println("✗", nodeErr)
	} else {
		fmt.Printf("✓ Node %s: %s\n", nodeVersion, nodePath)
	}
	if version, err := copilotRuntimeVersion(); err != nil {
		fmt.Println("✗ Copilot bridge runtime is not installed (run: cc-dialect copilot install)")
	} else if version != copilotSDKVersion {
		fmt.Printf("✗ @github/copilot-sdk %s is installed; %s is required (run: cc-dialect copilot install)\n",
			version, copilotSDKVersion)
	} else {
		fmt.Printf("✓ @github/copilot-sdk %s\n", version)
	}
	if nodeErr != nil {
		return nodeErr
	}
	status, err := copilotSDKStatus()
	if err != nil {
		fmt.Println("✗ GitHub Copilot authentication:", err)
		return nil
	}
	if status.IsAuthenticated {
		label := status.Login
		if label == "" {
			label = status.AuthType
		}
		fmt.Printf("✓ GitHub Copilot authenticated (%s)\n", label)
	} else {
		fmt.Println("✗ GitHub Copilot is not authenticated (run: cc-dialect copilot login)")
	}
	return nil
}

func copilotNode() (path, version string, err error) {
	path, err = exec.LookPath("node")
	if err != nil {
		return "", "", errors.New("Node.js was not found in PATH; Copilot support requires Node.js 22.13 or newer")
	}
	raw, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("read Node.js version: %w", err)
	}
	version = strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
	major, minor, ok := parseMajorMinor(version)
	if !ok || major < copilotMinNodeMajor || major == copilotMinNodeMajor && minor < copilotMinNodeMinor {
		return "", "", fmt.Errorf("Node.js %s is unsupported; Copilot support requires Node.js 22.13 or newer", version)
	}
	return path, version, nil
}

func copilotRuntimeVersion() (string, error) {
	_, _, packagePath, _, err := copilotRuntimePaths()
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
		return "", errors.New("installed @github/copilot-sdk package has no version")
	}
	return metadata.Version, nil
}

func requireCopilotRuntime() error {
	version, err := copilotRuntimeVersion()
	if err != nil {
		return errors.New("Copilot bridge runtime is not installed; run: cc-dialect copilot install")
	}
	if version != copilotSDKVersion {
		return fmt.Errorf("Copilot bridge has @github/copilot-sdk %s but this cc-dialect requires %s; run: cc-dialect copilot install",
			version, copilotSDKVersion)
	}
	return nil
}

func writeCopilotBridge(path string) error {
	return atomicWriteFile(path, copilotBridgeSource, 0o600)
}

type copilotAuthStatus struct {
	IsAuthenticated bool   `json:"isAuthenticated"`
	AuthType        string `json:"authType"`
	Login           string `json:"login"`
}

func copilotSDKStatus() (copilotAuthStatus, error) {
	var status copilotAuthStatus
	output, err := runCopilotSDKScript(`const value = await client.getAuthStatus(); process.stdout.write(JSON.stringify(value));`)
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(output, &status)
	return status, err
}

func copilotSDKModels() ([]string, error) {
	output, err := runCopilotSDKScript(`const items = await client.listModels(); process.stdout.write(JSON.stringify(items.filter((item) => item.policy?.state !== "disabled").map((item) => item.id)));`)
	if err != nil {
		return nil, err
	}
	var models []string
	if err = json.Unmarshal(output, &models); err != nil {
		return nil, fmt.Errorf("decode Copilot models: %w", err)
	}
	sort.Strings(models)
	return models, nil
}

func runCopilotSDKScript(action string) ([]byte, error) {
	if err := requireCopilotRuntime(); err != nil {
		return nil, err
	}
	nodePath, _, err := copilotNode()
	if err != nil {
		return nil, err
	}
	runtimeDir, _, _, _, err := copilotRuntimePaths()
	if err != nil {
		return nil, err
	}
	script := `import { CopilotClient } from "@github/copilot-sdk";
const client = new CopilotClient({ mode: "empty", baseDirectory: process.env.COPILOT_DIALECT_HOME, logLevel: "error" });
await client.start();
try { ` + action + ` } finally { await client.stop(); }`
	cmd := exec.Command(nodePath, "--input-type=module", "--eval", script)
	cmd.Dir = runtimeDir
	cmd.Env = copilotBridgeEnvironment("", filepath.Join(runtimeDir, "copilot-home"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("query GitHub Copilot SDK: %s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

func copilotBridgePID(name string) int {
	pidPath, _, _, _, err := copilotInstancePaths(name)
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

func copilotBridgeHealthy(dialect Dialect) bool {
	if dialect.Bridge != "copilot" || dialect.BridgePort == 0 {
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

func startCopilotBridge(name string, dialect Dialect) error {
	if dialect.Bridge != "copilot" {
		return nil
	}
	if copilotBridgeHealthy(dialect) {
		return nil
	}
	nodePath, _, err := copilotNode()
	if err != nil {
		return err
	}
	runtimeDir, bridgePath, _, _, err := copilotRuntimePaths()
	if err != nil {
		return err
	}
	if err = requireCopilotRuntime(); err != nil {
		return err
	}
	if err = writeCopilotBridge(bridgePath); err != nil {
		return err
	}
	pidPath, logPath, stateDir, versionPath, err := copilotInstancePaths(name)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	if pid := copilotBridgePID(name); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.BridgePort) {
			return fmt.Errorf("Copilot bridge process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`",
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
		"--state", stateDir,
	)
	cmd.Dir = runtimeDir
	cmd.Env = copilotBridgeEnvironment(dialect.APIKey, stateDir)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	if err = atomicWriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_ = atomicWriteFile(versionPath, []byte(CurrentAppVersion()+"\n"), 0o600)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if copilotBridgeHealthy(dialect) {
			return nil
		}
		if !processAlive(cmd.Process.Pid) {
			return fmt.Errorf("Copilot bridge exited during startup; see %s", logPath)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out starting Copilot bridge; see %s", logPath)
}

func copilotBridgeEnvironment(bridgeKey, stateDir string) []string {
	allowed := []string{
		"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SHELL", "USER", "LOGNAME",
		"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "COPILOT_GH_HOST",
	}
	env := make([]string, 0, len(allowed)+2)
	for _, key := range allowed {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "COPILOT_DIALECT_BRIDGE_KEY="+bridgeKey)
	env = append(env, "COPILOT_DIALECT_HOME="+stateDir)
	return env
}

func stopCopilotBridge(name string, dialect Dialect) error {
	if dialect.Bridge != "copilot" {
		return nil
	}
	pidPath, _, _, _, err := copilotInstancePaths(name)
	if err != nil {
		return err
	}
	pid := copilotBridgePID(name)
	if pid == 0 {
		return nil
	}
	if !copilotBridgeHealthy(dialect) {
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
