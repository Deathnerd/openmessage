package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/maxghenis/openmessage/internal/app"
)

// serveStop lets a service manager end a daemon `serve` run the way SIGTERM
// does on Unix. Windows services receive SCM stop requests, not signals.
var serveStop = make(chan struct{})

const (
	serviceLogName     = "daemon.log"
	serviceLogMaxBytes = 20 << 20
)

// openServiceLog opens <data dir>/daemon.log for append, rotating the previous
// file to daemon.log.1 once it passes serviceLogMaxBytes. A service has no
// console, so this file is the only place its log lands.
func openServiceLog() (*os.File, error) {
	dataDir := app.DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, serviceLogName)
	if info, err := os.Stat(path); err == nil && info.Size() > serviceLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open service log: %w", err)
	}
	return file, nil
}

func serviceLogger(file *os.File) zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: file, NoColor: true}).
		With().Timestamp().Logger().Level(LogLevel())
}
