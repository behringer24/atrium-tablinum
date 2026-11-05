package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// Knowledge types, responsible people and review dates (docs/design/knowledge-metadata.md).
func TestKnowledgeTypesOwnersAndReviews(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "dave@x.de": authz.Curator,
		"bob@x.de": authz.Reader, "sam@x.de": authz.Admin,
	})
	e.team(t, "hr", "HR", false, map[string]authz.Role{"cora@x.de": authz.Curator, "bob@x.de": authz.Reader})
	anna, cora, bob, sam, boss := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "sam@x.de"), e.session(t, "boss@x.de")
	e.session(t, "carl@x.de") // known, but no access to dev
	e.mustCall(t, e.session(t, "dave@x.de"), "list_spaces", nil)

	store := func(cs *mcp.ClientSession, args map[string]any) string {
		t.Helper()
		args["force"] = true
		return e.mustCall(t, cs, "store_knowledge", args)["id"].(string)
	}
	get := func(cs *mcp.ClientSession, id string) map[string]any {
		t.Helper()
		return e.mustCall(t, cs, "get_knowledge", map[string]any{"id": id})
	}
	fails := func(cs *mcp.ClientSession, tool string, args map[string]any, want string) {
		t.Helper()
		if _, msg := e.call(t, cs, tool, args); !strings.Contains(msg, want) {
			t.Errorf("%s %v: %q, want %q", tool, args, msg, want)
		}
	}

	// Types: authors file what they like except rules; the default is documentation.
	tips := store(anna, map[string]any{"title": "Review tips", "content": "Keep changes small.", "space": "dev", "type": "recommendation"})
	plain := store(anna, map[string]any{"title": "Branch naming", "content": "feat, fix and doc branches.", "space": "dev"})
	if got := get(bob, tips)["type"]; got != "recommendation" {
		t.Errorf("type of tips: %v", got)
	}
	if got := get(bob, plain)["type"]; got != "documentation" {
		t.Errorf("default type: %v", got)
	}
	fails(anna, "store_knowledge", map[string]any{"title": "Deploy policy", "content": "Two approvals.", "space": "dev", "type": "rule"}, "binding rules")
	fails(anna, "store_knowledge", map[string]any{"title": "X", "content": "Y", "space": "dev", "type": "law"}, "type must be")
	policy := store(cora, map[string]any{"title": "Deploy policy", "content": "Every deploy needs two approvals.", "space": "dev", "type": "rule"})

	// Search and lists carry the type and filter by it.
	hits := e.mustCall(t, bob, "search_knowledge", map[string]any{"query": "deploy approvals", "types": []string{"rule"}})["results"].([]any)
	if len(hits) == 0 {
		t.Fatal("no rule found")
	}
	for _, h := range hits {
		if h.(map[string]any)["type"] != "rule" {
			t.Errorf("type filter in search: %v", h)
		}
	}
	if list := e.mustCall(t, bob, "list_knowledge", map[string]any{"space": "dev", "type": "recommendation"})["entries"].([]any); len(list) != 1 {
		t.Errorf("type filter in list: %v", list)
	}

	// A rule is the curators' business: once an entry is a rule its author cannot change it any more.
	e.mustCall(t, cora, "update_knowledge", map[string]any{"id": plain, "type": "rule"})
	fails(anna, "update_knowledge", map[string]any{"id": plain, "content": "Anything goes."}, "binding rules")
	fails(anna, "update_knowledge", map[string]any{"id": plain, "type": "documentation"}, "binding rules")
	var types []string
	rows, err := e.srv.Pool.Query(e.ctx, `SELECT knowledge_type FROM document_versions WHERE document_id = $1 ORDER BY version`, plain)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ty string
		if err := rows.Scan(&ty); err != nil {
			t.Fatal(err)
		}
		types = append(types, ty)
	}
	rows.Close()
	if strings.Join(types, ",") != "documentation,rule" {
		t.Errorf("the versions record the type: %v", types)
	}

	// Responsible person: named on purpose, must be active and able to read the space.
	fails(anna, "update_knowledge", map[string]any{"id": tips, "owner": "carl@x.de"}, "cannot read this space")
	fails(anna, "update_knowledge", map[string]any{"id": tips, "owner": "nobody@x.de"}, "unknown")
	fails(bob, "store_knowledge", map[string]any{"title": "Mine", "content": "private", "owner": "bob@x.de"}, "team spaces only")
	before := get(anna, tips)["version"]
	out := e.mustCall(t, anna, "update_knowledge", map[string]any{"id": tips, "owner": "bob@x.de", "review_due": "2020-01-01"})
	if out["owner"] != "bob@x.de" || out["review_overdue"] != true || out["version"] != before {
		t.Errorf("owner and review date, without a new version: %v (version before %v)", out, before)
	}
	fails(anna, "update_knowledge", map[string]any{"id": tips, "review_due": "next week"}, "review_due must be a date")
	if due := e.mustCall(t, cora, "list_knowledge", map[string]any{"space": "dev", "review": "due"})["entries"].([]any); len(due) != 1 {
		t.Errorf("reviews due: %v", due)
	}
	waits := e.mustCall(t, cora, "whats_new", nil)["waiting_for_review"].([]any)
	if len(waits) != 1 || waits[0].(map[string]any)["reviews_due"] != float64(1) {
		t.Errorf("whats_new for curators: %v", waits)
	}
	hrNote := store(cora, map[string]any{"title": "Holiday calendar", "content": "Where the calendar lives.", "space": "hr", "owner": "bob@x.de"})

	// Offboarding keeps the names, marked former, and says what to reassign.
	off := e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "bob@x.de", "confirm": true})
	if off["responsible_for"] != float64(2) || !strings.Contains(off["next"].(string), "reassign_owner") {
		t.Errorf("offboarding: %v", off)
	}
	if got := get(anna, tips); got["owner"] != "bob@x.de" || got["owner_former"] != true {
		t.Errorf("a former owner stays named: %v", got)
	}
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "dave@x.de", "confirm": true})
	for _, sp := range e.mustCall(t, anna, "space_contacts", map[string]any{"space": "dev"})["spaces"].([]any) {
		for _, ct := range sp.(map[string]any)["contacts"].([]any) {
			m := ct.(map[string]any)
			if (m["name"] == "dave@x.de") != (m["former"] == true) {
				t.Errorf("contacts: %v", m)
			}
		}
	}

	// Reassigning: space admins for their spaces, never to someone locked or someone who cannot read.
	if res, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "reassign_owner", Arguments: map[string]any{"from": "bob@x.de", "to": "anna@x.de"}}); err == nil && !res.IsError {
		t.Error("an author reassigned")
	}
	fails(sam, "reassign_owner", map[string]any{"from": "bob@x.de", "to": "dave@x.de", "confirm": true}, "locked")
	fails(sam, "reassign_owner", map[string]any{"from": "bob@x.de", "to": "anna@x.de"}, "1 entries")
	moved := e.mustCall(t, sam, "reassign_owner", map[string]any{"from": "bob@x.de", "to": "anna@x.de", "confirm": true})
	if r := moved["reassigned"].([]any); len(r) != 1 || r[0].(map[string]any)["entries"] != float64(1) {
		t.Errorf("space admin reassigned: %v", moved)
	}
	if got := get(anna, tips); got["owner"] != "anna@x.de" || got["owner_former"] == true {
		t.Errorf("after reassigning: %v", got)
	}
	// the instance admin reaches HR too, but not for somebody who cannot read it
	moved = e.mustCall(t, boss, "reassign_owner", map[string]any{"from": "bob@x.de", "to": "anna@x.de", "confirm": true})
	if sk, _ := moved["skipped"].([]any); len(sk) != 1 || sk[0].(map[string]any)["space"] != "HR" {
		t.Errorf("skipped: %v", moved)
	}
	if got := get(cora, hrNote)["owner"]; got != "bob@x.de" {
		t.Errorf("an entry was handed to somebody who cannot read it: %v", got)
	}
	var n int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = 'owner_reassign'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit: %d %v", n, err)
	}
	_ = policy
}
