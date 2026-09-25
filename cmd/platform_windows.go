//go:build windows

package cmd

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows has no process umask; files are private via the user profile ACLs.
func setPrivateUmask() (restore func()) {
	return func() {}
}

// LockFileEx locks are mandatory on Windows, so lock a single byte far past
// EOF instead of the record itself; readers of instance.lock stay unblocked.
const lockOffsetHigh = 0x7fffffff

func lockFileExclusive(file *os.File) error {
	overlapped := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
}

func unlockFile(file *os.File) error {
	overlapped := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func isLockHeldError(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// syncDirectory is a no-op: Windows cannot fsync a directory handle, and NTFS
// journals renames itself.
func syncDirectory(string) error {
	return nil
}

func filesystemAvailableBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytesAvailable uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, nil, nil); err != nil {
		return 0, err
	}
	return freeBytesAvailable, nil
}
