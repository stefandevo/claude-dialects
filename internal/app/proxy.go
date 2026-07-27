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

func fetchBridgeModels(dialect Dialect) ([]string, error) {
	client := &http.Client{Timeout: 12 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", dialect.BridgePort), nil)
	req.Header.Set("Authorization", "Bearer "+dialect.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s model endpoint returned %s", dialect.Bridge, resp.Status)
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
	instance, err := openInstanceFS(name)
	if err != nil {
		return false
	}
	defer instance.Close()
	entries, err := instance.ReadDir("auth")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := instance.ReadFile(filepath.Join("auth", entry.Name()))
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

// missingAuthProviders returns the OAuth providers a dialect expects but has no
// credentials for yet, preserving the order declared by the dialect. A mixed
// dialect that maps tiers across providers returns every provider still needing
// `cc-dialect auth`; a fully authenticated or upstream/bridge dialect returns nil.
func missingAuthProviders(name string, dialect Dialect) []string {
	var missing []string
	for _, provider := range expectedAuthProviders(dialect) {
		if !hasProviderCredentials(name, provider) {
			missing = append(missing, provider)
		}
	}
	return missing
}

func proxyPID(name string) int {
	instance, err := openInstanceFS(name)
	if err != nil {
		return 0
	}
	defer instance.Close()
	return instance.runningPID("proxy.pid")
}

func startProxy(name string, dialect Dialect) (err error) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	// Pin the directory before anything is started. The handle resolves the
	// dialect name lazily, so without this the bridge below could be launched
	// against one directory and the proxy's own I/O land in another.
	if _, err = instance.ensureDir(); err != nil {
		return err
	}
	unwindBridge, err := startManagedBridge(instance, dialect)
	if err != nil {
		return err
	}
	if unwindBridge != nil {
		// The bridge is started before the proxy, so every proxy failure below
		// leaves a process this call launched listening with its PID in the pinned
		// directory — and if that directory is what moved, a later stop resolving
		// the name cannot find it at all. Unwind exactly what this call started; a
		// bridge that was already serving returns no closure and is left alone.
		defer func() {
			if err != nil {
				err = errors.Join(err, unwindBridge())
			}
		}()
	}
	if proxyHealthy(dialect) {
		// Health is answered by the port, which cannot say which directory the
		// process belongs to. A proxy left over from a directory replaced before
		// this call began answers with the same configured key, so the pin check
		// alone would pass — it only proves the pinned directory stayed put while
		// this call ran — and startup would succeed with no proxy.pid under the
		// pinned root at all. A later stop would read that absence as "already
		// stopped" and removal would drop the configuration while it still serves.
		// The ownership record is what ties the running proxy to this directory.
		pid, pidErr := instance.ReadPID("proxy.pid")
		if pidErr != nil {
			return fmt.Errorf("read the PID record for %q: %w", name, pidErr)
		}
		if pid == 0 || !processAlive(pid) {
			return fmt.Errorf(
				"a proxy is already serving port %d but dialect %q holds no record of owning it; stop it and start again",
				dialect.Port, name)
		}
		pinned, pinErr := instance.StillPinned()
		if pinErr == nil && pinned {
			return nil
		}
		return errors.Join(
			fmt.Errorf("dialect directory for %q changed while the proxy was starting", name), pinErr)
	}
	if pid := instance.runningPID("proxy.pid"); pid > 0 && processAlive(pid) {
		if !portAvailable(dialect.Port) {
			return fmt.Errorf("proxy process %d is alive but not responding on port %d; see `cc-dialect proxy %s logs`", pid, dialect.Port, name)
		}
		// The PID was reused by an unrelated process. Never signal it.
		_ = instance.RemoveIfExists("proxy.pid")
	}
	if !portAvailable(dialect.Port) {
		return fmt.Errorf("port %d for %q is already in use by another process", dialect.Port, name)
	}
	if _, err = writeProxyConfigAt(instance, dialect); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath, err := instance.Abs("proxy.log")
	if err != nil {
		return err
	}
	logFile, err := instance.OpenAppend("proxy.log", 0o600)
	if err != nil {
		return err
	}
	// The child re-resolves the dialect by name, so hand it the identity of the
	// directory pinned here: re-resolving a name can land somewhere else, and the
	// child refusing to serve from a directory this process did not pin is what
	// keeps the PID recorded below and the process it names in the same place.
	identity, err := instance.Identity()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__proxy", name)
	cmd.Env = append(os.Environ(), instanceIdentityEnv+"="+identity)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	exited := monitorStartedProcess(cmd)
	if err = instance.WritePID("proxy.pid", cmd.Process.Pid); err != nil {
		cleanupErr := cleanupStartedProcess(cmd, exited, instance, "proxy.pid")
		return errors.Join(fmt.Errorf("record proxy PID: %w", err), cleanupErr)
	}
	// The version sidecar is written by the __proxy child (runEmbeddedProxy):
	// exec.Command re-executes the on-disk binary, which can be newer than this
	// parent process, so only the child knows the identity actually serving.
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		select {
		case waitErr := <-exited:
			cleanupErr := instance.RemoveIfExists("proxy.pid")
			if waitErr == nil {
				return errors.Join(fmt.Errorf("embedded proxy exited during startup; see %s", logPath), cleanupErr)
			}
			return errors.Join(fmt.Errorf("embedded proxy exited during startup: %w; see %s", waitErr, logPath), cleanupErr)
		default:
		}
		if proxyHealthy(dialect) {
			// The child re-opened the instance by name to serve, while the PID
			// above was recorded through this root. If the directory the name
			// resolves to has changed since, the two no longer agree and a later
			// stop would find no PID and leave the proxy running — refuse the
			// startup instead of leaking a process nothing owns.
			pinned, pinErr := instance.StillPinned()
			if pinErr == nil && pinned {
				return nil
			}
			cleanupErr := cleanupStartedProcess(cmd, exited, instance, "proxy.pid")
			return errors.Join(
				fmt.Errorf("dialect directory for %q changed while the proxy was starting", name),
				pinErr, cleanupErr)
		}
		time.Sleep(150 * time.Millisecond)
	}
	cleanupErr := cleanupStartedProcess(cmd, exited, instance, "proxy.pid")
	return errors.Join(fmt.Errorf("timed out starting embedded proxy; see %s", logPath), cleanupErr)
}

func stopProxy(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	dialect, exists := cfg.Dialects[name]
	if !exists {
		// Without the private key there is no safe way to prove PID ownership.
		return instance.RemoveIfExists("proxy.pid")
	}
	return stopProxyDialect(instance, dialect)
}

// stopProxyDialectByName stops a dialect for callers holding no pinned handle of
// their own. Callers that already pinned the directory — removal above all —
// must pass that handle to stopProxyDialect instead, so the stop and whatever
// follows it act on one directory rather than on whatever the name resolves to
// at each step.
func stopProxyDialectByName(name string, dialect Dialect) error {
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	return stopProxyDialect(instance, dialect)
}

// stopProxyDialect stops the proxy and any managed bridge belonging to the
// directory instance is pinned to. Every step reads through that one handle: the
// bridge stop used to re-open the dialect by name, so an entry replaced midway
// let the proxy be stopped from the original directory while the replacement's
// absent bridge PID read as "already stopped", leaving the original bridge
// running behind a successful stop.
func stopProxyDialect(instance *instanceFS, dialect Dialect) (err error) {
	name := instance.name
	defer func() {
		err = errors.Join(err, stopManagedBridge(instance, dialect))
	}()
	pid, pidErr := instance.ReadPID("proxy.pid")
	if pidErr != nil {
		// The ownership record cannot be read — the instance or the PID path was
		// replaced with something the root refuses to follow. Reporting success
		// here would let RemoveDialect drop the config and unlink the entry while
		// the proxy keeps serving, with nothing left pointing at it.
		//
		// Liveness is judged by the port, not by the health endpoint: a proxy that
		// is wedged or still starting fails the health probe while very much alive,
		// and would be abandoned. A dead one has released its port, which is what
		// keeps a tampered-but-stopped dialect removable.
		if portBusy(dialect.Port) {
			return fmt.Errorf("proxy for %q still holds port %d but its PID record cannot be read safely: %w", name, dialect.Port, pidErr)
		}
		return nil
	}
	if pid == 0 {
		return nil
	}
	if !proxyHealthy(dialect) {
		// A stale PID can refer to an unrelated process after reboot or PID reuse.
		// Only signal a process that answers with this dialect's private API key.
		return instance.RemoveIfExists("proxy.pid")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if processAlive(pid) {
		if err = process.Signal(os.Interrupt); err != nil {
			return err
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && processAlive(pid); {
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(pid) {
			if err = process.Kill(); err != nil {
				return err
			}
		}
	}
	return instance.RemoveIfExists("proxy.pid")
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
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	// Only the dialect name crosses the spawn boundary, and re-resolving a name
	// can land on a different directory than the parent pinned. Refuse to serve
	// from anywhere else: the parent records this process's PID through its own
	// pinned root, so serving from elsewhere would leave the running proxy and
	// its ownership record in two different directories.
	if expected := os.Getenv(instanceIdentityEnv); expected != "" {
		if err = instance.MatchesIdentity(expected); err != nil {
			return err
		}
	}
	// Stamp this process's own identity before serving: the spawning parent may
	// be an older cc-dialect build that re-executed a newer on-disk binary, so
	// only this child knows which build is actually running the proxy. The stamp
	// is best-effort — doctor reads a missing marker as an unknown build and
	// prompts a restart, which is a better outcome than refusing to serve.
	_ = instance.WriteBuildIdentity("proxy.version")
	path, err := writeProxyConfigAt(instance, dialect)
	if err != nil {
		return err
	}
	proxyCfg, err := readProxyConfigAt(instance)
	if err != nil {
		return err
	}
	// CLIProxyAPI requires the absolute config path for its long-lived file
	// watcher. The initial read is confined above; later watcher/auth persistence
	// is an external pathname-based trust boundary, not an os.Root operation.
	service, err := cliproxy.NewBuilder().WithConfig(proxyCfg).WithConfigPath(path).Build()
	if err != nil {
		return err
	}
	// Observe how full each request leaves the dialect's context window. This is
	// diagnostics only: Claude Code remains solely responsible for /compact and
	// auto-compaction, and the proxy never rewrites a conversation.
	service.RegisterUsagePlugin(newContextMonitor(name, dialect))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err = service.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func readProxyConfigAt(instance *instanceFS) (*proxyconfig.Config, error) {
	data, err := instance.ReadFile("proxy.yaml")
	if err != nil {
		return nil, err
	}
	return proxyconfig.ParseConfigBytes(data)
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
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	if _, err = writeProxyConfigAt(instance, dialect); err != nil {
		return err
	}
	proxyCfg, err := readProxyConfigAt(instance)
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
	// CLIProxyAPI's provider token serializers accept only absolute pathname
	// strings. Rooted setup above prevents a pre-existing escape; the dependency's
	// write remains an external pathname-based trust boundary.
	_, saved, err := manager.Login(context.Background(), provider, proxyCfg, &proxyauth.LoginOptions{
		NoBrowser: noBrowser,
		Prompt:    prompt,
	})
	if err != nil {
		return err
	}
	fmt.Println("Authenticated", provider)
	if saved != "" {
		// CLIProxyAPI has already written the token by now, through the absolute
		// auth-dir rather than this root, so the check below is a detection and
		// not a confinement — see the limits documented in README "Files and
		// security". It compares file identity rather than the pathname, because
		// a path that reads as being inside the dialect proves nothing if a
		// component was replaced while the dependency held it.
		rel, relErr := instance.RelUnder(saved, "auth")
		if relErr != nil {
			return fmt.Errorf("credentials were written outside dialect %q; inspect and remove %s: %w", name, saved, relErr)
		}
		if confirmErr := instance.ConfirmWrittenInside(rel, saved); confirmErr != nil {
			return fmt.Errorf("credentials were written outside dialect %q; inspect and remove %s: %w", name, saved, confirmErr)
		}
		if chmodErr := instance.Chmod(rel, 0o600); chmodErr != nil {
			return fmt.Errorf("secure saved credentials: %w", chmodErr)
		}
		fmt.Println("Credentials:", saved)
	}
	if proxyHealthy(dialect) {
		fmt.Println("Restarting proxy to load the new credentials…")
		_, err = newAppService().RestartDialect(name)
		return err
	}
	return nil
}

func tailLog(name string) error {
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	logs := []struct {
		path  string
		label string
	}{
		{path: "proxy.log", label: "embedded proxy"},
		{path: "cursor-bridge.log", label: "Cursor bridge"},
		{path: "copilot-bridge.log", label: "Copilot bridge"},
	}
	printed := false
	for _, log := range logs {
		file, openErr := instance.Open(log.path)
		if openErr != nil {
			continue
		}
		fmt.Printf("== %s ==\n", log.label)
		_, copyErr := io.Copy(os.Stdout, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		printed = true
	}
	if !printed {
		return fmt.Errorf("no logs found for %q", name)
	}
	return nil
}

// startManagedBridge takes the caller's already-pinned instance rather than
// opening its own: a second handle would resolve the dialect name again, so a
// directory replaced in between would leave the bridge and its PID in one
// directory while the proxy serves from another.
//
// It returns a closure undoing the bridge this call launched, or nil when it
// launched nothing, which is what lets a caller unwind its own bridge on a later
// failure without touching one that was already serving.
func startManagedBridge(instance *instanceFS, dialect Dialect) (func() error, error) {
	switch dialect.Bridge {
	case "":
		return nil, nil
	case "cursor":
		return startCursorBridge(instance, dialect)
	case "copilot":
		return startCopilotBridge(instance, dialect)
	default:
		return nil, fmt.Errorf("unsupported managed bridge %q", dialect.Bridge)
	}
}

// stopManagedBridge mirrors startManagedBridge: it reuses the caller's pinned
// instance rather than opening its own, so the bridge is stopped in the same
// directory the proxy was.
func stopManagedBridge(instance *instanceFS, dialect Dialect) error {
	switch dialect.Bridge {
	case "":
		return nil
	case "cursor":
		return stopCursorBridge(instance, dialect)
	case "copilot":
		return stopCopilotBridge(instance, dialect)
	default:
		return nil
	}
}

func managedBridgeHealthy(dialect Dialect) bool {
	switch dialect.Bridge {
	case "":
		return true
	case "cursor":
		return cursorBridgeHealthy(dialect)
	case "copilot":
		return copilotBridgeHealthy(dialect)
	default:
		return false
	}
}

func managedBridgePID(name string, dialect Dialect) int {
	switch dialect.Bridge {
	case "cursor":
		return cursorBridgePID(name)
	case "copilot":
		return copilotBridgePID(name)
	default:
		return 0
	}
}
