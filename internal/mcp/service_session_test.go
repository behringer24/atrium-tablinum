package mcpserver

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// What a service account can do in this edition, once it has a token of its own: it works with the role it was
// given and nothing more, writes and reads like anybody else, and shows in the audit log under its own client name.
func TestServiceAccountSession(t *testing.T) {
	e := setup(t, 0)
	given := e.team(t, "handbuch", "Handbuch", false, nil)
	other := e.team(t, "intern", "Intern", false, nil)
	open := e.team(t, "offen", "Offen", false, nil)
	author := authz.Author
	if _, err := e.spaces.Update(e.ctx, open.ID, spaces.UpdateParams{DefaultRole: &author}); err != nil {
		t.Fatal(err)
	}
	id := e.serviceAccount(t, "docs-sync", map[uuid.UUID]authz.Role{given.ID: authz.Author})
	svc := e.serviceSession(t, id, "docs-sync")
	boss := e.session(t, "boss@x.de")

	// The default role of an open space does not reach it, an explicit role does.
	out := e.mustCall(t, svc, "list_spaces", nil)
	var slugs []string
	for _, sp := range out["spaces"].([]any) {
		slugs = append(slugs, sp.(map[string]any)["slug"].(string))
	}
	if strings.Join(slugs, ",") != "handbuch" {
		t.Errorf("spaces of the service account: %v (the open space must not be among them, nor the others)", slugs)
	}
	res := e.mustCall(t, svc, "store_knowledge", map[string]any{"space": "handbuch", "title": "From the tool", "content": "Written by a service account."})
	if res["result"] != "stored" {
		t.Fatalf("store: %v", res)
	}
	if res, _ := e.call(t, svc, "store_knowledge", map[string]any{"space": "offen", "title": "Nope", "content": "Not allowed here."}); res["result"] == "stored" {
		t.Errorf("stored into a space it has no role in: %v", res)
	}
	_ = other

	// No tool of a curator, space admin or instance admin without the role, whatever the tool list shows.
	if names := toolNames(t, e, svc); names["admin_create_space"] || names["admin_rights_report"] || names["admin_instance_admins"] {
		t.Errorf("instance admin tools for a service account: %v", names)
	}

	// The audit log names the token, not the client's own claim.
	log := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "store"})
	found := false
	for _, ent := range log["entries"].([]any) {
		if ent.(map[string]any)["client"] == "token:docs-sync" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit entries: %v", log["entries"])
	}
	// The entry carries the same name as its source.
	if got := e.mustCall(t, boss, "list_knowledge", map[string]any{"space": "handbuch"}); !strings.Contains(toJSON(t, got), "token:docs-sync") {
		t.Errorf("source of the entry: %v", got)
	}
}

// The write limit of a service account is its own: a burst of writes does not use up the limit of people.
func TestServiceAccountHasItsOwnWriteLimit(t *testing.T) {
	e := setup(t, 2) // people: two writes a minute
	e.srv.svcLimiter = newLimiter(5, e.srv.limiter.window)
	sp := e.team(t, "handbuch", "Handbuch", false, map[string]authz.Role{"anna@x.de": authz.Author})
	id := e.serviceAccount(t, "docs-sync", map[uuid.UUID]authz.Role{sp.ID: authz.Author})
	svc := e.serviceSession(t, id, "docs-sync")
	anna := e.session(t, "anna@x.de")

	write := func(cs *mcp.ClientSession, n int) string {
		_, msg := e.call(t, cs, "store_knowledge", map[string]any{"space": "handbuch", "title": "Entry " + uuid.NewString()[:8], "content": "Text " + uuid.NewString(), "force": true})
		return msg
	}
	for i := 0; i < 5; i++ {
		if msg := write(svc, i); msg != "" {
			t.Fatalf("write %d of the service account: %s", i+1, msg)
		}
	}
	if msg := write(svc, 5); !strings.Contains(msg, "too many") {
		t.Errorf("the sixth write of the service account: %q", msg)
	}
	// Anna still has her two.
	if msg := write(anna, 0); msg != "" {
		t.Errorf("first write of a person: %s", msg)
	}
}
