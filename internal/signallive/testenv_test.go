package signallive

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// setTestTempDir points os.TempDir at dir on every platform: Unix reads
// TMPDIR, Windows reads TMP (then TEMP).
func setTestTempDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
}

// writeSignalCLIStub writes an executable stand-in for signal-cli: a shell
// script on Unix, a batch file on Windows (which cannot run #! scripts).
func writeSignalCLIStub(t *testing.T, unixScript, windowsScript string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "signal-cli-stub")
	script := unixScript
	if runtime.GOOS == "windows" {
		stub += ".cmd"
		script = windowsScript
	}
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}
