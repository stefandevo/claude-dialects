package app

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	buildIdentityOnce   sync.Once
	buildIdentityCached string
)

// appBuildIdentity returns the deterministic identity stamped next to proxy and
// bridge PID files at spawn time and compared by `doctor` to detect runtimes
// started by an older cc-dialect build. Release builds use the injected version
// verbatim; dev builds (the default `make install` path stamps VERSION=dev)
// derive the identity from the binary content instead, so `git pull && make
// install` always produces a distinct stamp.
//
// The identity is computed once per process — `Run` warms it at startup so the
// hash reflects the binary this process was started from, not whatever later
// replaces it on disk.
func appBuildIdentity() string {
	buildIdentityOnce.Do(func() {
		buildIdentityCached = buildIdentity(CurrentAppVersion(), os.Executable)
	})
	return buildIdentityCached
}

func buildIdentity(version string, executable func() (string, error)) string {
	if version != "" && version != "dev" {
		return version
	}
	path, err := executable()
	if err != nil {
		return "dev"
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	file, err := os.Open(path)
	if err != nil {
		return "dev"
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "dev"
	}
	return "dev-" + hex.EncodeToString(digest.Sum(nil))[:16]
}

func proxySpawnVersion(name string) string {
	_, _, _, _, _, _, versionPath, err := paths(name)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func cursorBridgeSpawnVersion(name string) string {
	_, _, _, versionPath, err := cursorInstancePaths(name)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func copilotBridgeSpawnVersion(name string) string {
	_, _, _, versionPath, err := copilotInstancePaths(name)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
