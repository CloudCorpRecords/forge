package main

import (
	"errors"
	"os"

	"github.com/git-pkgs/forge/internal/cli"
)

// exitCoder is implemented by command errors that carry a process exit code.
type exitCoder interface {
	ExitCode() int
}

func main() {
	if err := cli.Execute(); err != nil {
		var ec exitCoder
		if errors.As(err, &ec) {
			os.Exit(ec.ExitCode())
		}
		os.Exit(1)
	}
}
