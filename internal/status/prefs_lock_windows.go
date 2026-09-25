//go:build windows

package status

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive blocks until f holds an exclusive byte-range lock over
// its first byte, the Windows counterpart of flock(LOCK_EX).
func lockFileExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}

func unlockFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}
