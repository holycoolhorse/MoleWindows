//go:build !windows

package main

const uninstallSupported = false

func runUninstall([]string) int { return 1 }
