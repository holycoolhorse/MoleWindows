//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
)

func runAnalyze() int {
	fmt.Fprintln(os.Stderr, "analyze is only supported on macOS and Windows")
	return 1
}
