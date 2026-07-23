cat << 'INNER_EOF' > temp_patch.go
var currentAppVersion string

// SetAppVersion is called by main.go to store the current binary version
func SetAppVersion(version string) {
	currentAppVersion = version
}

// CurrentAppVersion returns the version stored by main.go, or the build info fallback
func CurrentAppVersion() string {
	if currentAppVersion != "" {
		return currentAppVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	return info.Main.Version
}

INNER_EOF
sed -i '' -e '/^func embeddedProxyVersion() string {/i\
' internal/app/cli.go
sed -i '' -e '/^func embeddedProxyVersion() string {/r temp_patch.go' internal/app/cli.go
rm temp_patch.go
