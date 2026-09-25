//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// ServiceName is the Windows service name the SCM knows the daemon by.
const ServiceName = "OpenMessage"

// serviceStopTimeout bounds how long a stop waits for serve's deferred
// cleanup. libgm RPCs have no deadline and ignore Disconnect, so a stop that
// lands mid-sync can block cleanup indefinitely; the process then exits
// without it. That is safe: SQLite in WAL mode keeps every committed write
// across a process exit, and backfill resumes on the next start.
const serviceStopTimeout = 10 * time.Second

// RunService runs `serve` under the Windows Service Control Manager. It is
// the service's binPath entry point (`openmessage.exe service [serve flags]`)
// and refuses to run from a console, where `serve` is the right command.
func RunService(_ zerolog.Logger, args ...string) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect service context: %w", err)
	}
	if !isService {
		return errors.New("`service` is started by the Windows Service Control Manager; run `openmessage serve` for a foreground daemon")
	}
	logFile, _, err := openServiceLog()
	if err != nil {
		return err
	}
	defer logFile.Close()
	// A service has no console, so stderr is discarded. Point it at the log:
	// serve prints the web UI bootstrap URL there, and the Go runtime writes
	// fatal panics through the process's STD_ERROR_HANDLE.
	os.Stderr = logFile
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(logFile.Fd())); err != nil {
		fmt.Fprintf(logFile, "redirect STD_ERROR_HANDLE: %v\n", err)
	}
	logger := serviceLogger(logFile)
	logger.Info().Str("service", ServiceName).Strs("args", args).Msg("Service starting")
	if err := svc.Run(ServiceName, &daemonService{logger: logger, args: args}); err != nil {
		logger.Error().Err(err).Msg("Service dispatcher failed")
		return err
	}
	return nil
}

type daemonService struct {
	logger zerolog.Logger
	args   []string
}

func (s *daemonService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("serve panicked: %v", r)
			}
		}()
		done <- RunServe(s.logger, s.args...)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			// serve exited without being asked to. A non-zero exit code lets
			// the SCM recovery actions (restart on failure) kick in.
			if err != nil {
				s.logger.Error().Err(err).Msg("serve exited with an error")
				return false, 1
			}
			s.logger.Warn().Msg("serve exited unexpectedly")
			return false, 1
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s.logger.Info().Msg("Service stop requested")
				status <- svc.Status{State: svc.StopPending}
				close(serveStop)
				select {
				case err := <-done:
					if err != nil {
						s.logger.Error().Err(err).Msg("serve returned an error while stopping")
					}
				case <-time.After(serviceStopTimeout):
					s.logger.Warn().Dur("timeout", serviceStopTimeout).Msg("serve cleanup still blocked (usually an in-flight Google RPC); exiting without it")
					// At debug level, dump every goroutine so an unexpected hang
					// is diagnosable from daemon.log alone.
					if s.logger.GetLevel() <= zerolog.DebugLevel {
						stacks := make([]byte, 1<<20)
						stacks = stacks[:runtime.Stack(stacks, true)]
						s.logger.Debug().Msg("goroutine dump:\n" + string(stacks))
					}
				}
				s.logger.Info().Msg("Service stopped")
				return false, 0
			}
		}
	}
}
