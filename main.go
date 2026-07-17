package main

import (
	"fmt"
	"os"

	"github.com/stefandevo/claude-dialects/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
