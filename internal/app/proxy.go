package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	proxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func proxyHealthy(dialect Dialect) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", dialect.Port), nil)
	req.Header.Set("Authorization", "Bearer "+dialect.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func fetchModels(dialect Dialect) ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", dialect.Port), nil)
	req.Header.Set("Authorization", "Bearer "+dialect.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model endpoint returned %s", resp.Status)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(body.Data))
	for _, model := range body.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func hasProviderCredentials(name, provider string) bool {
	_, _, _, authDir, _, _, err := paths(name)
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(authDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(authDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var metadata struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &metadata) == nil && strings.EqualFold(metadata.Type, provider) {
			return true
		}
	}
	return false
}

func proxyPID(name string) int {
	_, _, _, _, pidPath, _, err := paths(name)
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

func startProxy(name string, dialect Dialect) error {
	if err := startCursorBridge(name, dialect); err != nil {
		return err
	}
	if proxyHealthy(dialect) {
		return nil
	}
	if pid := proxyPID(name); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.Port) {
			return fmt.Errorf("proxy process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`", pid, dialect.Port, name)
		}
		// The PID was reused by an unrelated process. Never signal it.
		_, _, _, _, pidPath, _, _ := paths(name)
		_ = os.Remove(pidPath)
	}
	if !portAvailable(dialect.Port) {
		return fmt.Errorf("port %d for %q is already in use by another process", dialect.Port, name)
	}
	if _, err := writeProxyConfig(name, dialect); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_, _, _, _, pidPath, logPath, err := paths(name)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__proxy", name)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
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
		if proxyHealthy(dialect) {
			return nil
		}
		if !processAlive(cmd.Process.Pid) {
			return fmt.Errorf("embedded proxy exited during startup; see %s", logPath)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out starting embedded proxy; see %s", logPath)
}

func stopProxy(name string) error {
	cfg, cfgErr := loadConfig()
	var dialect Dialect
	var exists bool
	if cfgErr == nil {
		dialect, exists = cfg.Dialects[name]
	}
	if exists {
		defer func() {
			_ = stopCursorBridge(name, dialect)
		}()
	}
	pid := proxyPID(name)
	if pid == 0 {
		return nil
	}
	if cfgErr != nil || !exists || !proxyHealthy(dialect) {
		// A stale PID can refer to an unrelated process after reboot or PID reuse.
		// Only signal a process that answers with this dialect's private API key.
		_, _, _, _, pidPath, _, _ := paths(name)
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
	_, _, _, _, pidPath, _, _ := paths(name)
	_ = os.Remove(pidPath)
	return nil
}

func runEmbeddedProxy(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dialect, ok := cfg.Dialects[name]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", name)
	}
	path, err := writeProxyConfig(name, dialect)
	if err != nil {
		return err
	}
	proxyCfg, err := proxyconfig.LoadConfig(path)
	if err != nil {
		return err
	}
	service, err := cliproxy.NewBuilder().WithConfig(proxyCfg).WithConfigPath(path).Build()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err = service.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func authenticate(name, provider string, noBrowser bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dialect, ok := cfg.Dialects[name]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", name)
	}
	if dialect.BaseURL != "" {
		return fmt.Errorf("dialect %q uses upstream API authentication via %s; OAuth login is not needed", name, dialect.AuthTokenEnv)
	}
	path, err := writeProxyConfig(name, dialect)
	if err != nil {
		return err
	}
	proxyCfg, err := proxyconfig.LoadConfig(path)
	if err != nil {
		return err
	}
	store := proxyauth.NewFileTokenStore()
	manager := proxyauth.NewManager(store,
		proxyauth.NewCodexAuthenticator(),
		proxyauth.NewClaudeAuthenticator(),
		proxyauth.NewAntigravityAuthenticator(),
		proxyauth.NewKimiAuthenticator(),
		proxyauth.NewXAIAuthenticator(),
	)
	prompt := func(label string) (string, error) {
		fmt.Fprint(os.Stderr, label)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimSpace(line), err
	}
	_, saved, err := manager.Login(context.Background(), provider, proxyCfg, &proxyauth.LoginOptions{
		NoBrowser: noBrowser,
		Prompt:    prompt,
	})
	if err != nil {
		return err
	}
	fmt.Println("Authenticated", provider)
	if saved != "" {
		if chmodErr := os.Chmod(saved, 0o600); chmodErr != nil {
			return fmt.Errorf("secure saved credentials: %w", chmodErr)
		}
		fmt.Println("Credentials:", saved)
	}
	if proxyHealthy(dialect) {
		fmt.Println("Restarting proxy to load the new credentials…")
		_ = stopProxy(name)
		return startProxy(name, dialect)
	}
	return nil
}

func tailLog(name string) error {
	_, _, _, _, _, path, err := paths(name)
	if err != nil {
		return err
	}
	printed := false
	if file, openErr := os.Open(path); openErr == nil {
		fmt.Println("== embedded proxy ==")
		_, err = io.Copy(os.Stdout, file)
		_ = file.Close()
		if err != nil {
			return err
		}
		printed = true
	}
	_, cursorLog, _, cursorErr := cursorInstancePaths(name)
	if cursorErr == nil {
		if file, openErr := os.Open(cursorLog); openErr == nil {
			fmt.Println("== Cursor bridge ==")
			_, err = io.Copy(os.Stdout, file)
			_ = file.Close()
			if err != nil {
				return err
			}
			printed = true
		}
	}
	if !printed {
		return fmt.Errorf("no logs found for %q", name)
	}
	return nil
}
