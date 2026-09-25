//go:build !windows

package cmd

import "errors"

// RunService is Windows-only; Unix daemons run `serve` under launchd/systemd.
func RunService(...string) error {
	return errors.New("`service` is only supported on Windows; run `openmessage serve` under launchd or systemd")
}
