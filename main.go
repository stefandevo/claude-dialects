package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/stefandevo/claude-dialects/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(os.Args[1:], version); err != nil {
		var exitError *app.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.Code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
