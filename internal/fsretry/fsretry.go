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
	// retryBudget bounds the total wait. Conflicts usually clear within
	// milliseconds, but concurrent renames over one target can queue for
	// hundreds; 250ms measurably flaked under TestSaveSessionRewritesAtomically.
	retryBudget  = time.Second
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
	deadline := time.Now().Add(retryBudget)
	delay := initialDelay
	for {
		err := op()
		if err == nil || !isTransientSharingError(err) || time.Now().Add(delay).After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, maxDelay)
	}
}
