//go:build !windows

package main

const cleanSupported = false

func runClean([]string) int { return 1 }
