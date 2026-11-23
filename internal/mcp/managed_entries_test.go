package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// An entry that a service account keeps up to date says so, and nobody else changes or moves it through the
// tools; the refusal is audited.
func TestManagedEntriesInTheTools(t *testing.T) {
	e := setup(t, 0)
	sp := e.team(t, "code", "Code", false, map[string]authz.Role{"cora@x.de": authz.Curator})
	e.team(t, "ziel", "Ziel", false, map[string]authz.Role{"cora@x.de": authz.Curator})
	id := e.serviceAccount(t, "docs-sync", map[uuid.UUID]authz.Role{sp.ID: authz.Curator})
	cora := e.session(t, "cora@x.de")
	boss := e.session(t, "boss@x.de")

	u, err := e.srv.Users.Get(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := e.srv.Checker.EffectiveRoles(e.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.srv.Docs.Sync(e.ctx, docs.Actor{User: u, Roles: roles, Client: "token:docs-sync"}, docs.SyncParams{
		SpaceID: sp.ID, Key: "git:repo:a.md", Title: "A file", Content: "Text of the file.",
		Origin: docs.Origin{Ref: "git://repo/a.md", SHA256: strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	entry := res.Document.ID.String()

	got := e.mustCall(t, cora, "get_knowledge", map[string]any{"id": entry})
	if got["managed_by"] != "docs-sync@service.invalid" || got["sync_key"] != "git:repo:a.md" {
		t.Errorf("get_knowledge does not say who keeps the entry up to date: %v", got)
	}
	if plain := e.mustCall(t, cora, "store_knowledge", map[string]any{"space": "code", "title": "By hand", "content": "A normal entry.", "force": true}); plain["id"] == nil {
		t.Fatalf("store: %v", plain)
	}
	list := e.mustCall(t, cora, "list_knowledge", map[string]any{"space": "code"})
	for _, d := range list["entries"].([]any) {
		m := d.(map[string]any)
		if (m["title"] == "By hand") != (m["managed_by"] == nil) {
			t.Errorf("only the synced entry names an account: %v", m)
		}
	}

	_, msg := e.call(t, cora, "update_knowledge", map[string]any{"id": entry, "content": "Changed by a person."})
	if !strings.Contains(msg, "kept up to date") {
		t.Errorf("update by a curator: %q", msg)
	}
	_, msg = e.call(t, cora, "move_knowledge", map[string]any{"id": entry, "space": "ziel", "confirm": true})
	if !strings.Contains(msg, "kept up to date") {
		t.Errorf("move by a curator: %q", msg)
	}
	// Archiving stays possible.
	if _, msg := e.call(t, cora, "archive_knowledge", map[string]any{"id": entry, "confirm": true}); msg != "" {
		t.Errorf("archive by a curator: %q", msg)
	}
	log := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "denied"})
	found := false
	for _, ent := range log["entries"].([]any) {
		if strings.Contains(toJSON(t, ent), "kept up to date") {
			found = true
		}
	}
	if !found {
		t.Errorf("the refusal is not in the audit log: %v", log["entries"])
	}
}

// A tool of the service tier appears for service accounts only.
func TestServiceTierToolsAreForServiceAccounts(t *testing.T) {
	saved := editionTools
	t.Cleanup(func() { editionTools = saved })
	type in struct{}
	type out struct {
		Message string `json:"message"`
	}
	editionDescriptions["service_probe"] = "A tool for service accounts."
	t.Cleanup(func() { delete(editionDescriptions, "service_probe") })
	editionTools = append(editionTools, func(s *Server, ms *mcp.Server, group tier) {
		if group == tierService {
			addTool(s, ms, "service_probe", "Probe", tierService, false, func(_ context.Context, _ *call, _ in) (out, error) {
				return out{Message: "ok"}, nil
			})
		}
	})
	e := setup(t, 0)
	sp := e.team(t, "code", "Code", false, nil)
	svc := e.serviceSession(t, e.serviceAccount(t, "docs-sync", map[uuid.UUID]authz.Role{sp.ID: authz.Author}), "docs-sync")
	boss := e.session(t, "boss@x.de")
	if !toolNames(t, e, svc)["service_probe"] {
		t.Error("the service account does not see the service tool")
	}
	if toolNames(t, e, boss)["service_probe"] {
		t.Error("an instance admin sees the service tool")
	}
	if res := e.mustCall(t, svc, "service_probe", nil); res["message"] != "ok" {
		t.Errorf("service tool: %v", res)
	}
}
