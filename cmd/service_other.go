//go:build !windows

package cmd

import (
	"errors"

	"github.com/rs/zerolog"
)

// RunService is Windows-only; Unix daemons run `serve` under launchd/systemd.
func RunService(_ zerolog.Logger, _ ...string) error {
	return errors.New("`service` is only supported on Windows; run `openmessage serve` under launchd or systemd")
}
