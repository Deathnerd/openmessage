package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// maxBridgeLine bounds one JSON-RPC message from the MCP host (tool calls
// carrying media can be large).
const maxBridgeLine = 32 << 20

// RunMCPBridge relays MCP between stdio and a remote OpenMessage server's
// streamable HTTP endpoint (`openmessage mcp-bridge --url URL --token-file
// PATH`). It exists for MCP hosts that can only launch local servers, such as
// Claude Desktop, whose remote connectors run from Anthropic's cloud and
// cannot reach a LAN/tailnet-only server.
func RunMCPBridge(args ...string) error {
	url, token, err := parseMCPBridgeArgs(args)
	if err != nil {
		return err
	}
	return runMCPBridge(context.Background(), os.Stdin, os.Stdout, url, token)
}

func parseMCPBridgeArgs(args []string) (url, token string, err error) {
	var tokenFile string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--url", "--token-file":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("%s requires a value", args[i])
			}
			if args[i] == "--url" {
				url = args[i+1]
			} else {
				tokenFile = args[i+1]
			}
			i++
		default:
			return "", "", fmt.Errorf("unknown mcp-bridge option %s", args[i])
		}
	}
	if url == "" || tokenFile == "" {
		return "", "", errors.New("usage: openmessage mcp-bridge --url <https://host/mcp> --token-file <path>")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", "", fmt.Errorf("read token file: %w", err)
	}
	return url, strings.TrimSpace(string(raw)), nil
}

// runMCPBridge forwards each JSON-RPC line from in to the server and writes
// replies and server notifications to out, one JSON object per line. A
// request that fails at the transport level (unreachable, 401) gets a JSON-RPC
// error reply, so the host reports it instead of hanging. It returns when in
// reaches EOF and in-flight requests have finished.
func runMCPBridge(ctx context.Context, in io.Reader, out io.Writer, url, token string) error {
	httpTransport, err := transport.NewStreamableHTTP(url,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		return fmt.Errorf("create MCP transport: %w", err)
	}
	if err := httpTransport.Start(ctx); err != nil {
		return fmt.Errorf("start MCP transport: %w", err)
	}
	defer httpTransport.Close()

	var writeMu sync.Mutex
	write := func(msg any) {
		b, err := json.Marshal(msg)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	httpTransport.SetNotificationHandler(func(n mcp.JSONRPCNotification) { write(n) })

	serverError := func(err error) *mcp.JSONRPCErrorDetails {
		return &mcp.JSONRPCErrorDetails{
			Code:    mcp.INTERNAL_ERROR,
			Message: fmt.Sprintf("OpenMessage server at %s: %v", url, err),
		}
	}
	forward := func(req transport.JSONRPCRequest) {
		resp, err := httpTransport.SendRequest(ctx, req)
		if err != nil {
			write(transport.JSONRPCResponse{JSONRPC: mcp.JSONRPC_VERSION, ID: req.ID, Error: serverError(err)})
			return
		}
		write(resp)
	}

	var inFlight sync.WaitGroup
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), maxBridgeLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg struct {
			ID     *mcp.RequestId  `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &msg); err != nil || msg.Method == "" {
			continue // not a request or notification (e.g. a reply to a server request)
		}
		if msg.ID == nil {
			var notification mcp.JSONRPCNotification
			if err := json.Unmarshal(line, &notification); err == nil {
				_ = httpTransport.SendNotification(ctx, notification)
			}
			continue
		}
		req := transport.JSONRPCRequest{JSONRPC: mcp.JSONRPC_VERSION, ID: *msg.ID, Method: msg.Method}
		if len(msg.Params) > 0 {
			req.Params = msg.Params
		}
		// initialize establishes the session every later request needs, so
		// it runs inline; everything else runs concurrently so a slow tool
		// call does not block the host's other requests.
		if msg.Method == string(mcp.MethodInitialize) {
			forward(req)
			continue
		}
		inFlight.Add(1)
		go func() {
			defer inFlight.Done()
			forward(req)
		}()
	}
	inFlight.Wait()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP host input: %w", err)
	}
	return nil
}
