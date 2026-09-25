// Package fsretry wraps the file operations behind atomic replace-by-rename
// so they tolerate Windows sharing semantics.
//
// On Unix, renaming over a file that another goroutine or process has open
// always succeeds, and readers keep the old inode. On Windows, os.Open does
// not grant FILE_SHARE_DELETE, so a rename that lands while a reader holds
// the target fails with ERROR_ACCESS_DENIED, and a reader that opens during
// the replace fails with ERROR_SHARING_VIOLATION. Both are transient: they
// clear as soon as the other side closes its handle.
package fsretry

import (
	"os"
	"time"
)

const (
	maxAttempts  = 50
	initialDelay = time.Millisecond
	maxDelay     = 20 * time.Millisecond
)

// Rename is os.Rename, retried briefly on transient Windows sharing errors.
func Rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) })
}

// ReadFile is os.ReadFile, retried briefly on transient Windows sharing errors.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retry(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

func retry(op func() error) error {
	delay := initialDelay
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = op(); err == nil || !isTransientSharingError(err) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, maxDelay)
	}
	return err
}
