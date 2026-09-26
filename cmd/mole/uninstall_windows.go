//go:build windows

package main

import (
	"os"

	"github.com/tw93/mole/internal/uninstall"
)

const uninstallSupported = true

func runUninstall(args []string) int {
	return uninstall.Main(args, os.Stdin, os.Stdout, os.Stderr)
}
