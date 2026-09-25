//go:build darwin || windows

package main

import "github.com/tw93/mole/internal/analyze"

func runAnalyze() int {
	analyze.Main()
	return 0
}
