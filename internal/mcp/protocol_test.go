package mcpserver

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// post sends one raw JSON-RPC request to /mcp as the given user.
func (e *env) post(t *testing.T, email string, headers map[string]string, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.http.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok-"+email)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The tool list and the instructions depend on the caller's roles, so no answer that carries cache
// hints may be marked as shareable, and none may be fresh for any time: a change of rights has to
// show at the next request.
func TestCacheHintsArePrivate(t *testing.T) {
	e := setup(t, 0)
	e.session(t, "anna@x.de")

	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"probe","version":"1"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}}`
	for _, method := range []string{"server/discover", "tools/list", "resources/templates/list"} {
		status, body := e.post(t, "anna@x.de",
			map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method},
			`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":{`+meta+`}}`)
		if status != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %.300s", method, status, body)
		}
		if !strings.Contains(body, `"cacheScope":"private"`) || !strings.Contains(body, `"ttlMs":0`) || strings.Contains(body, `"public"`) {
			t.Errorf("%s: cache hints: %.400s", method, body)
		}
	}
}

// Clients of the sessionless protocol (2026-07-28) send no initialize and no session id: protocol
// version, client info and capabilities travel with every request. A stateful server rejects
// them, so the endpoint has to stay stateless.
func TestEndpointAcceptsSessionlessProtocol(t *testing.T) {
	e := setup(t, 0)
	e.session(t, "anna@x.de") // creates the account and the test token

	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"probe","version":"1"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}}`
	status, body := e.post(t, "anna@x.de",
		map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+meta+`}}`)
	if status != http.StatusOK || !strings.Contains(body, `"list_spaces"`) {
		t.Fatalf("sessionless tools/list: HTTP %d: %.300s", status, body)
	}

	// Older clients (initialize, then calls) are served as before, without a session id.
	status, body = e.post(t, "anna@x.de", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`)
	if status != http.StatusOK || !strings.Contains(body, `"result"`) {
		t.Fatalf("initialize: HTTP %d: %.300s", status, body)
	}
	status, body = e.post(t, "anna@x.de", map[string]string{"MCP-Protocol-Version": "2025-06-18"},
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK || !strings.Contains(body, `"list_spaces"`) {
		t.Fatalf("tools/list without session: HTTP %d: %.300s", status, body)
	}

	// Without a token nothing is served.
	req, _ := http.NewRequest(http.MethodPost, e.http.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without token: %v %v", resp, err)
	}
}
