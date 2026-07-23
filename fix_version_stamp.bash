sed -i '' 's/_ = atomicWriteFile(versionPath, \[\]byte(embeddedProxyVersion()+"\\n"), 0o600)/_ = atomicWriteFile(versionPath, \[\]byte(CurrentAppVersion()+"\\n"), 0o600)/' internal/app/proxy.go
sed -i '' 's/_ = atomicWriteFile(versionPath, \[\]byte(embeddedProxyVersion()+"\\n"), 0o600)/_ = atomicWriteFile(versionPath, \[\]byte(CurrentAppVersion()+"\\n"), 0o600)/' internal/app/cursor.go
sed -i '' 's/_ = atomicWriteFile(versionPath, \[\]byte(embeddedProxyVersion()+"\\n"), 0o600)/_ = atomicWriteFile(versionPath, \[\]byte(CurrentAppVersion()+"\\n"), 0o600)/' internal/app/copilot.go
