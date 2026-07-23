BEGIN { in_doctor = 0; in_fix = 0 }
/^func doctor/ { in_doctor = 1 }
/if \*fix {/ { 
    if (in_doctor) {
        in_fix = 1
        print "\tif *fix {"
        print "\t\tvar fixErrors []error"
        next
    }
}
/_ = copilotCommand/ {
    if (in_fix) {
        print "\t\t\tif err := copilotCommand([]string{\"install\"}); err != nil {"
        print "\t\t\t\tfixErrors = append(fixErrors, fmt.Errorf(\"Copilot install failed: %w\", err))"
        print "\t\t\t}"
        next
    }
}
/_ = cursorCommand/ {
    if (in_fix) {
        print "\t\t\tif err := cursorCommand([]string{\"install\"}); err != nil {"
        print "\t\t\t\tfixErrors = append(fixErrors, fmt.Errorf(\"Cursor install failed: %w\", err))"
        print "\t\t\t}"
        next
    }
}
/fmt.Printf\("Failed to restart %s: %v\\n", name, err\)/ {
    if (in_fix) {
        print "\t\t\t\tfixErrors = append(fixErrors, fmt.Errorf(\"failed to restart %s: %w\", name, err))"
        next
    }
}
/return nil/ {
    if (in_fix && in_doctor) {
        print "\t\tif len(fixErrors) > 0 {"
        print "\t\t\tfmt.Println(\"\\n✗ Some fixes failed to apply:\")"
        print "\t\t\tfor _, err := range fixErrors {"
        print "\t\t\t\tfmt.Printf(\"  - %v\\n\", err)"
        print "\t\t\t}"
        print "\t\t\treturn errors.New(\"one or more deterministic fixes failed\")"
        print "\t\t}"
        print "\t}"
        print "\treturn nil"
        in_fix = 0
        in_doctor = 0
        next
    }
}
{ print }
