package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/notify"
)

func TestAdminNotificationsIsOffUntilConfigured(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"author@x.de": authz.Author})
	boss := e.session(t, "boss@x.de")
	out := e.mustCall(t, boss, "admin_notifications", map[string]any{"action": "get"})
	if msg, _ := out["message"].(string); !strings.Contains(msg, "Notifications are off") || len(out["connections"].([]any)) != 0 {
		t.Errorf("off: %v", out)
	}
	if toolNames(t, e, e.session(t, "author@x.de"))["admin_notifications"] {
		t.Errorf("a regular user must not see admin_notifications")
	}
	// The tool is not even known to a regular user's session.
	res, err := e.session(t, "author@x.de").CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_notifications", Arguments: map[string]any{"action": "get"}})
	if err == nil && !res.IsError {
		t.Errorf("a regular user must be refused")
	}
}

func TestAdminNotificationsChoosesAndTests(t *testing.T) {
	e := setup(t, 0)
	var mu sync.Mutex
	var posts []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		posts = append(posts, string(b))
		mu.Unlock()
	}))
	defer hook.Close()
	var mails []string
	e.srv.Notify = notify.New(e.srv.Pool, e.srv.Users, e.srv.Checker, e.srv.Spaces, notify.Options{
		Enabled: true, Instance: "Atrium", AllowPrivateNetworks: true, MaxWebhooks: 1, MaxMail: 1,
		Connections: []notify.Connection{
			{Name: "chat", Type: "slack", URL: hook.URL + "/services/SECRETPART"},
			{Name: "chat2", Type: "slack", URL: hook.URL + "/services/other"},
			{Name: "mail", Type: "smtp", Host: "mail.test", Port: 587, TLS: "starttls", From: "atrium@x.de"},
		},
		Mailer: func(_ context.Context, _ notify.Connection, to string, msg []byte) error {
			mails = append(mails, to)
			return nil
		},
	})
	boss := e.session(t, "boss@x.de")

	out := e.mustCall(t, boss, "admin_notifications", map[string]any{"action": "get"})
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "SECRETPART") || strings.Contains(string(raw), hook.URL) {
		t.Fatalf("an address must never be shown: %s", raw)
	}
	conns := out["connections"].([]any)
	if len(conns) != 2 || conns[0].(map[string]any)["state"] != "off" {
		t.Errorf("connections: %v", conns)
	}
	if nu, _ := out["not_used"].([]any); len(nu) != 1 || nu[0] != "chat2" {
		t.Errorf("not_used: %v", out["not_used"])
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "chat2") {
		t.Errorf("the message must say which connections are not used: %q", msg)
	}

	// Changing needs the user's confirmation, an event and a known connection.
	set := map[string]any{"action": "set", "connection": "chat", "events": []string{"draft_waiting", "health_changed"}, "enabled": true}
	if _, msg := e.call(t, boss, "admin_notifications", set); !strings.Contains(msg, "confirm") {
		t.Errorf("without confirm: %q", msg)
	}
	set["confirm"] = true
	set["events"] = []string{"everything"}
	if _, msg := e.call(t, boss, "admin_notifications", set); !strings.Contains(msg, "unknown event") {
		t.Errorf("unknown event: %q", msg)
	}
	set["events"] = []string{"draft_waiting", "health_changed"}
	set["connection"] = "chat2"
	if _, msg := e.call(t, boss, "admin_notifications", set); !strings.Contains(msg, "no such connection") {
		t.Errorf("a connection that is not used: %q", msg)
	}
	set["connection"] = "chat"
	out = e.mustCall(t, boss, "admin_notifications", set)
	first := out["connections"].([]any)[0].(map[string]any)
	if first["state"] != "on" || len(first["events"].([]any)) != 2 {
		t.Errorf("after set: %v", first)
	}
	var audited int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = 'admin_notifications_set'`).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("the change must be audited once: %d %v", audited, err)
	}

	// A test needs confirmation too; a mail goes to the caller's own address.
	if _, msg := e.call(t, boss, "admin_notifications", map[string]any{"action": "test", "connection": "chat"}); !strings.Contains(msg, "confirm") {
		t.Errorf("test without confirm: %q", msg)
	}
	e.mustCall(t, boss, "admin_notifications", map[string]any{"action": "test", "connection": "chat", "confirm": true})
	e.mustCall(t, boss, "admin_notifications", map[string]any{"action": "test", "connection": "mail", "confirm": true})
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || !strings.Contains(posts[0], "test notice") {
		t.Errorf("web hook posts: %v", posts)
	}
	if len(mails) != 1 || mails[0] != "boss@x.de" {
		t.Errorf("mails: %v", mails)
	}
}
