package app

import (
	"os"
	"strings"
)

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
