package web

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	envAllowedHosts     = "OPENMESSAGES_ALLOWED_HOSTS"
	envControlTokenFile = "OPENMESSAGES_CONTROL_TOKEN_FILE"
	minRemoteTokenLen   = 32
	healthPath          = "/healthz"
)

// RemoteAccess serves the daemon to non-loopback clients, for a shared
// server (e.g. in Kubernetes behind an ingress). Unlike local mode, which
// only warns about missing credentials (accept-and-log), every request except
// /healthz must carry the bearer token, and the Host header must be loopback
// or one of the allowed hosts, which keeps the DNS-rebinding defence of
// ProtectLocalControl for named hosts.
type RemoteAccess struct {
	hosts map[string]struct{}
	token string
}

// LoadRemoteAccess reads remote mode configuration from the environment. It
// returns nil when OPENMESSAGES_ALLOWED_HOSTS is unset (local mode), and an
// error when remote mode is requested without a usable token file.
func LoadRemoteAccess(getenv func(string) string) (*RemoteAccess, error) {
	hosts := make(map[string]struct{})
	for _, host := range strings.Split(getenv(envAllowedHosts), ",") {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			hosts[host] = struct{}{}
		}
	}
	if len(hosts) == 0 {
		return nil, nil
	}
	tokenPath := strings.TrimSpace(getenv(envControlTokenFile))
	if tokenPath == "" {
		return nil, fmt.Errorf("remote mode (%s) requires %s", envAllowedHosts, envControlTokenFile)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", envControlTokenFile, err)
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < minRemoteTokenLen {
		return nil, fmt.Errorf("control token in %s must be at least %d characters", tokenPath, minRemoteTokenLen)
	}
	return &RemoteAccess{hosts: hosts, token: token}, nil
}

// Handler enforces the host allowlist and bearer token in front of next, and
// answers /healthz itself so probes need neither.
func (r *RemoteAccess) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == healthPath {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if !r.hostAllowed(req.Host) {
			httpError(w, "forbidden: unexpected Host", http.StatusForbidden)
			return
		}
		if !r.authorized(req) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="openmessage"`)
			httpError(w, "unauthorized: bearer token required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (r *RemoteAccess) hostAllowed(hostport string) bool {
	host, _, ok := splitHostPortDefault(hostport, "")
	if !ok || host == "" {
		return false
	}
	if isLoopbackHost(host) {
		return true
	}
	_, allowed := r.hosts[strings.ToLower(host)]
	return allowed
}

func (r *RemoteAccess) authorized(req *http.Request) bool {
	token, ok := bearerToken(req)
	return ok && secureEqual(token, r.token)
}
