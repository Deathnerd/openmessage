//go:build windows

package fsretry

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isTransientSharingError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
