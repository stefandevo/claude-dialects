/<code>cc-dialect doctor<\/code>/ {
    print "            <code>cc-dialect doctor [--fix]</code>"
    next
}
/<span>Diagnose configuration issues<\/span>/ {
    print "            <span>Diagnose configuration issues and apply deterministic repairs</span>"
    next
}
{ print }
