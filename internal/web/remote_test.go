package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRemoteToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func writeTokenFile(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func remoteEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadRemoteAccessDisabledWithoutAllowedHosts(t *testing.T) {
	remote, err := LoadRemoteAccess(remoteEnv(nil))
	if err != nil {
		t.Fatalf("LoadRemoteAccess(): %v", err)
	}
	if remote != nil {
		t.Fatalf("remote access enabled without OPENMESSAGES_ALLOWED_HOSTS")
	}
}

func TestLoadRemoteAccessRejectsMissingOrWeakToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no token file", map[string]string{"OPENMESSAGES_ALLOWED_HOSTS": "om.example"}, "OPENMESSAGES_CONTROL_TOKEN_FILE"},
		{"unreadable file", map[string]string{"OPENMESSAGES_ALLOWED_HOSTS": "om.example", "OPENMESSAGES_CONTROL_TOKEN_FILE": filepath.Join(t.TempDir(), "missing")}, "read"},
		{"short token", map[string]string{"OPENMESSAGES_ALLOWED_HOSTS": "om.example", "OPENMESSAGES_CONTROL_TOKEN_FILE": writeTokenFile(t, "short")}, "at least"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRemoteAccess(remoteEnv(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadRemoteAccess() error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestRemoteAccessHandler(t *testing.T) {
	remote, err := LoadRemoteAccess(remoteEnv(map[string]string{
		"OPENMESSAGES_ALLOWED_HOSTS":      " om.example , Other.Example ",
		"OPENMESSAGES_CONTROL_TOKEN_FILE": writeTokenFile(t, testRemoteToken),
	}))
	if err != nil || remote == nil {
		t.Fatalf("LoadRemoteAccess() = %v, %v", remote, err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("mcp"))
	})
	handler := remote.Handler(next)

	for _, tc := range []struct {
		name, host, path, auth string
		want                   int
	}{
		{"healthz needs no auth or host", "10.42.0.7:7007", "/healthz", "", http.StatusOK},
		{"allowed host with token", "om.example", "/mcp", "Bearer " + testRemoteToken, http.StatusOK},
		{"allowed host is case-insensitive, port ignored", "OTHER.example:443", "/mcp", "Bearer " + testRemoteToken, http.StatusOK},
		{"loopback with token", "127.0.0.1:7007", "/mcp", "Bearer " + testRemoteToken, http.StatusOK},
		{"allowed host without token", "om.example", "/mcp", "", http.StatusUnauthorized},
		{"allowed host with wrong token", "om.example", "/mcp", "Bearer " + strings.Repeat("0", 64), http.StatusUnauthorized},
		{"non-bearer scheme", "om.example", "/mcp", "Basic " + testRemoteToken, http.StatusUnauthorized},
		{"unlisted host even with token", "evil.example", "/mcp", "Bearer " + testRemoteToken, http.StatusForbidden},
		{"missing host", "", "/mcp", "Bearer " + testRemoteToken, http.StatusForbidden},
		{"other paths also need auth", "om.example", "/api/status", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://placeholder"+tc.path, nil)
			req.Host = tc.host
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("401 without WWW-Authenticate header")
			}
			if tc.want != http.StatusOK && strings.Contains(rec.Body.String(), "mcp") {
				t.Fatalf("rejected request reached the wrapped handler")
			}
		})
	}
}
