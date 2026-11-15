package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// An edition may switch off the automatic content access of instance admins (authz.AdminsReadContent).
// Then an instance admin reads the spaces they are a member of like anybody else, but still manages
// every space: structure, members, archiving. Adding themselves as a member is the way in, and it is
// recorded.
func TestInstanceAdminWithoutAutomaticContentAccess(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "hr", "HR", false, map[string]authz.Role{"anna@x.de": authz.Author})
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	id := e.mustCall(t, anna, "store_knowledge", map[string]any{"space": "hr", "title": "Salary bands", "content": "Confidential salary bands."})["id"].(string)

	readable := func() bool {
		_, msg := e.call(t, boss, "get_knowledge", map[string]any{"id": id})
		return msg == ""
	}
	withSwitch := func(adminsRead bool) {
		old := authz.AdminsReadContent
		authz.AdminsReadContent = func(context.Context, *pgxpool.Pool) bool { return adminsRead }
		t.Cleanup(func() { authz.AdminsReadContent = old })
		e.srv.Checker.InvalidateAll()
	}

	if !readable() {
		t.Fatal("by default an instance admin reads every team space")
	}

	withSwitch(false)
	if readable() {
		t.Fatal("without automatic content access the instance admin still reads the space")
	}
	if hits, _ := e.call(t, boss, "search_knowledge", map[string]any{"query": "salary bands"}); strings.Contains(toJSON(t, hits), "Salary bands") {
		t.Error("the search finds content of a space the instance admin is no member of")
	}
	// The management of the space is unaffected.
	e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "hr", "require_approval": true})
	if out := e.mustCall(t, boss, "space_members", map[string]any{"space": "hr"}); out["members"] == nil {
		t.Errorf("space_members: %v", out)
	}
	e.mustCall(t, boss, "admin_archive_space", map[string]any{"space": "hr", "confirm": true})
	e.mustCall(t, boss, "admin_restore_space", map[string]any{"space": "hr"})
	if readable() {
		t.Fatal("archiving and restoring gave the instance admin access to the content")
	}

	// The way in is to become a member, and it is in the audit log.
	e.mustCall(t, boss, "space_add_member", map[string]any{"space": "hr", "user_email": "boss@x.de", "role": "reader"})
	if !readable() {
		t.Fatal("a member (reader) cannot read after adding themselves")
	}
	log := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "space_add_member"})
	if len(log["entries"].([]any)) == 0 {
		t.Error("the instance admin adding themselves is not in the audit log")
	}

	withSwitch(true)
	if !readable() {
		t.Fatal("switching back does not restore the access")
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
