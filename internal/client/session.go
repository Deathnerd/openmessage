package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maxghenis/openmessage/internal/fsretry"
)

type SessionData struct {
	AuthDataJSON json.RawMessage `json:"auth_data"`
	PushKeysJSON json.RawMessage `json:"push_keys,omitempty"`
}

// SaveSession marshals data and writes it with WriteSessionFile.
func SaveSession(path string, data *SessionData) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return WriteSessionFile(path, b)
}

// WriteSessionFile writes session.json atomically: the bytes go to a
// same-directory temp file that is fsynced and then renamed over the target,
// so a reader (or a crash) never sees a half-written file. It is the only
// writer of session.json; readers use ReadSessionFile.
//
// This matters because session.json holds the live Google/WhatsApp/Signal
// credentials and is no longer a rarely-touched file: rotated Google cookies
// are persisted every few minutes (see EventHandler.maybePersistRotatedCookies),
// so an in-place rewrite would put a truncation window in front of the paired
// auth several hundred times a day. Losing it costs a manual re-pair.
func WriteSessionFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("secure dir: %w", err)
	}
	// A unique temp name (not a fixed session.json.tmp) keeps two concurrent
	// savers from renaming each other's partial writes into place.
	tmp, err := os.CreateTemp(dir, ".session-*.json")
	if err != nil {
		return fmt.Errorf("create temp session: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename succeeds
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temp session: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	// Sync before the rename. Without it a power loss can commit the rename
	// while the contents are still only in the page cache, which lands exactly
	// the empty/truncated session.json the rename is meant to prevent.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp session: %w", err)
	}
	if err := fsretry.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install session: %w", err)
	}
	return nil
}

// ReadSessionFile reads session.json, retrying briefly while a concurrent
// WriteSessionFile rename holds it (a Windows sharing violation).
func ReadSessionFile(path string) ([]byte, error) {
	return fsretry.ReadFile(path)
}

func LoadSession(path string) (*SessionData, error) {
	b, err := ReadSessionFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var data SessionData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &data, nil
}
