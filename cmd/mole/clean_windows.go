//go:build windows

package main

import (
	"os"

	"github.com/tw93/mole/internal/clean"
)

const cleanSupported = true

func runClean(args []string) int {
	return clean.Main(args, os.Stdin, os.Stdout, os.Stderr)
}
