//go:build !windows

package fsretry

// Unix rename/open never conflict with open handles, so nothing is transient.
func isTransientSharingError(error) bool { return false }
