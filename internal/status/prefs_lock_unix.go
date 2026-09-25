//go:build !windows

package status

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFileExclusive blocks until f holds an exclusive advisory lock.
func lockFileExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
