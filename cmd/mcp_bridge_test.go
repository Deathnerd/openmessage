package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/maxghenis/openmessage/internal/web"
)

const bridgeTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newBridgeTestServer serves a one-tool MCP server through the production
// MCP handler behind remote-mode auth, and records the MCP-Protocol-Version
// header of every request after the first.
func newBridgeTestServer(t *testing.T) (url string, protocolVersions *[]string) {
	t.Helper()
	srv := mcpserver.NewMCPServer("bridge-test", "1.0.0")
	srv.AddTool(mcp.NewTool("echo", mcp.WithString("text", mcp.Required())),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("echo: " + req.GetString("text", "")), nil
		})
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(bridgeTestToken), 0o600); err != nil {
		t.Fatal(err)
	}
	remote, err := web.LoadRemoteAccess(func(key string) string {
		return map[string]string{
			"OPENMESSAGES_ALLOWED_HOSTS":      "openmessage.test",
			"OPENMESSAGES_CONTROL_TOKEN_FILE": tokenPath,
		}[key]
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu       sync.Mutex
		seen     int
		versions []string
	)
	handler := remote.Handler(newMCPHTTPHandler(srv, ""))
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			if seen++; seen > 1 {
				versions = append(versions, r.Header.Get("MCP-Protocol-Version"))
			}
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL + "/mcp", &versions
}

const bridgeTestSession = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":"call-3","method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}
`

// runBridgeForTest pipes input through the bridge and returns its output
// messages keyed by JSON-RPC id.
func runBridgeForTest(t *testing.T, url, token, input string) map[string]map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := runMCPBridge(ctx, strings.NewReader(input), &out, url, token); err != nil {
		t.Fatalf("runMCPBridge(): %v", err)
	}
	byID := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bridge wrote a non-JSON line %q: %v", line, err)
		}
		id, _ := json.Marshal(msg["id"])
		byID[string(id)] = msg
	}
	return byID
}

// requireJSONRPCErrors asserts every request in bridgeTestSession got a
// JSON-RPC error reply under its own id, with a message containing want.
func requireJSONRPCErrors(t *testing.T, byID map[string]map[string]any, want string) {
	t.Helper()
	for _, id := range []string{"1", "2", `"call-3"`} {
		errObj, ok := byID[id]["error"].(map[string]any)
		if !ok {
			t.Fatalf("id %s: want a JSON-RPC error, got %v (all: %v)", id, byID[id], byID)
		}
		if msg, _ := errObj["message"].(string); !strings.Contains(msg, want) {
			t.Fatalf("id %s: error message %q should contain %q", id, msg, want)
		}
	}
}

func TestMCPBridgeRelaysToolsOverStreamableHTTP(t *testing.T) {
	url, protocolVersions := newBridgeTestServer(t)
	byID := runBridgeForTest(t, url, bridgeTestToken, bridgeTestSession)

	initResult, _ := byID["1"]["result"].(map[string]any)
	if initResult == nil {
		t.Fatalf("initialize: no result: %v", byID["1"])
	}
	negotiated, _ := initResult["protocolVersion"].(string)
	if len(*protocolVersions) == 0 {
		t.Fatalf("no requests after initialize reached the server")
	}
	for _, got := range *protocolVersions {
		if negotiated == "" || got != negotiated {
			t.Fatalf("MCP-Protocol-Version after initialize = %q, want negotiated %q", got, negotiated)
		}
	}
	tools, _ := byID["2"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools/list = %v, want the echo tool", byID["2"])
	}
	call, _ := json.Marshal(byID[`"call-3"`]["result"])
	if !strings.Contains(string(call), "echo: hi") {
		t.Fatalf("tools/call result = %s, want echo: hi (string ids must round-trip)", call)
	}
}

func TestMCPBridgeReportsAuthFailureAsJSONRPCError(t *testing.T) {
	url, _ := newBridgeTestServer(t)
	byID := runBridgeForTest(t, url, strings.Repeat("f", 64), bridgeTestSession)
	requireJSONRPCErrors(t, byID, "HTTP 401")
}

func TestMCPBridgeReportsUnreachableServer(t *testing.T) {
	dead := httptest.NewServer(nil)
	url := dead.URL + "/mcp"
	dead.Close()

	byID := runBridgeForTest(t, url, bridgeTestToken, bridgeTestSession)
	errObj, ok := byID["2"]["error"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list against a dead server: want a JSON-RPC error, got %v", byID["2"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, url) {
		t.Fatalf("error %q should name the unreachable URL %s", msg, url)
	}
}

func TestParseMCPBridgeArgs(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" "+bridgeTestToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url, token, err := parseMCPBridgeArgs([]string{"--url", "https://om.example/mcp", "--token-file", tokenPath})
	if err != nil || url != "https://om.example/mcp" || token != bridgeTestToken {
		t.Fatalf("parseMCPBridgeArgs() = %q, %q, %v", url, token, err)
	}
	for _, args := range [][]string{
		{"--token-file", tokenPath},
		{"--url", "https://om.example/mcp"},
		{"--url", "https://om.example/mcp", "--token-file", filepath.Join(t.TempDir(), "missing")},
		{"--url", "https://om.example/mcp", "--token-file", tokenPath, "--bogus"},
	} {
		if _, _, err := parseMCPBridgeArgs(args); err == nil {
			t.Fatalf("parseMCPBridgeArgs(%q): want an error", args)
		}
	}
}

func TestMCPBridgeReportsNonJSONRPCErrorBodies(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"bad gateway"}`))
	}))
	t.Cleanup(proxy.Close)

	byID := runBridgeForTest(t, proxy.URL+"/mcp", bridgeTestToken, bridgeTestSession)
	requireJSONRPCErrors(t, byID, "HTTP 502")
}
