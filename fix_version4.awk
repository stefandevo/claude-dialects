BEGIN { in_embedded = 0; }
/^func embeddedProxyVersion\(\) string {/ {
    print "var currentAppVersion string"
    print ""
    print "// SetAppVersion is called by main.go to store the current binary version"
    print "func SetAppVersion(version string) {"
    print "\tcurrentAppVersion = version"
    print "}"
    print ""
    print "// CurrentAppVersion returns the version stored by main.go, or the build info fallback"
    print "func CurrentAppVersion() string {"
    print "\tif currentAppVersion != \"\" {"
    print "\t\treturn currentAppVersion"
    print "\t}"
    print "\tinfo, ok := debug.ReadBuildInfo()"
    print "\tif !ok || info.Main.Version == \"\" || info.Main.Version == \"(devel)\" {"
    print "\t\treturn \"dev\""
    print "\t}"
    print "\treturn info.Main.Version"
    print "}"
    print ""
    print "func embeddedProxyVersion() string {"
    next
}
{ print }
