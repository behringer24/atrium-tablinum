package mcpserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/license"
	"github.com/behringer24/atrium-tablinum/internal/product"
	"github.com/behringer24/atrium-tablinum/internal/quota"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func toolNames(t *testing.T, e *env, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

func TestToolListFollowsRole(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"spaceadmin@x.de": authz.Admin, "author@x.de": authz.Author, "cur@x.de": authz.Curator})

	plain := toolNames(t, e, e.session(t, "author@x.de"))
	curator := toolNames(t, e, e.session(t, "cur@x.de"))
	sadmin := toolNames(t, e, e.session(t, "spaceadmin@x.de"))
	boss := toolNames(t, e, e.session(t, "boss@x.de"))

	if plain["space_members"] || plain["admin_create_space"] || plain["review_knowledge"] || !plain["store_knowledge"] {
		t.Errorf("regular user: %v", plain)
	}
	if !curator["review_knowledge"] || curator["space_members"] || curator["admin_create_space"] {
		t.Errorf("curator: %v", curator)
	}
	if !sadmin["review_knowledge"] || !boss["review_knowledge"] {
		t.Error("space admin and instance admin without review_knowledge")
	}
	// Admin of one's own personal space is not enough for space admin tools (see 'plain')
	if !sadmin["space_members"] || !sadmin["space_add_member"] || !sadmin["space_remove_member"] || sadmin["admin_create_space"] || sadmin["admin_update_space"] || sadmin["admin_audit_log"] {
		t.Errorf("space admin: %v", sadmin)
	}
	for _, name := range []string{"admin_create_space", "admin_update_space", "admin_archive_space", "admin_restore_space", "admin_list_users",
		"admin_offboard_user", "admin_transfer_personal_space", "admin_reactivate_user", "admin_groups", "admin_audit_log", "admin_health", "space_members"} {
		if !boss[name] {
			t.Errorf("instance admin without %s", name)
		}
	}
}

func TestToolTierIsRecheckedOnEveryCall(t *testing.T) {
	e := setup(t, 0)
	anna := e.session(t, "anna@x.de")
	u, _ := e.users.GetByEmail(e.ctx, "anna@x.de")
	if err := e.users.SetInstanceAdmin(e.ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	cs := e.session(t, "anna@x.de") // a new session sees the admin tools
	if !toolNames(t, e, cs)["admin_health"] {
		t.Fatal("admin tools missing after appointment")
	}
	e.mustCall(t, cs, "admin_health", nil)
	if err := e.users.SetInstanceAdmin(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	// The endpoint is stateless: the tool list is built on every call from the current
	// permissions, so a revoked admin tool is gone immediately.
	if _, err := cs.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_health"}); err == nil {
		t.Error("admin rights revoked: tool still callable")
	}
	// The permission check in the tool itself remains as a second safeguard.
	boss, err := e.users.GetByEmail(e.ctx, "anna@x.de")
	if err != nil {
		t.Fatal(err)
	}
	if e.srv.allowed(e.ctx, tierInstanceAdmin, docs.Actor{User: boss}) {
		t.Error("allowed() lets a non-admin through")
	}
	// A tool the session never had does not exist for the client
	if _, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_health"}); err == nil {
		t.Error("unregistered tool callable")
	}
}

func TestSpaceAdminManagesOnlyOwnSpaces(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"sa@x.de": authz.Admin, "member@x.de": authz.Reader})
	e.team(t, "gf", "Geschäftsführung", false, map[string]authz.Role{"boss@x.de": authz.Admin})
	if _, err := e.users.CreateGroup(e.ctx, "Vertrieb"); err != nil {
		t.Fatal(err)
	}
	e.session(t, "newbie@x.de") // account exists but is in no space
	sa := e.session(t, "sa@x.de")

	e.mustCall(t, sa, "space_add_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "role": "author"})
	e.mustCall(t, sa, "space_add_member", map[string]any{"space": "support", "group": "vertrieb", "role": "reader"})
	e.mustCall(t, sa, "space_add_member", map[string]any{"space": "support", "user_email": "member@x.de", "role": "curator"}) // change role

	got := e.mustCall(t, sa, "space_members", map[string]any{"space": "Support"})
	byName := map[string]string{}
	for _, m := range got["members"].([]any) {
		mm := m.(map[string]any)
		byName[mm["name"].(string)] = mm["type"].(string) + ":" + mm["role"].(string)
	}
	if byName["newbie@x.de"] != "user:author" || byName["Vertrieb"] != "group:reader" || byName["member@x.de"] != "user:curator" || len(byName) != 4 {
		t.Errorf("members: %v", byName)
	}
	// the newly added author can now save there
	newbie := e.session(t, "newbie@x.de")
	if out := e.mustCall(t, newbie, "store_knowledge", map[string]any{"title": "T", "content": "x", "space": "support"}); out["result"] != "stored" {
		t.Errorf("new author: %v", out)
	}

	// foreign space: same answer as for a nonexistent one
	for _, space := range []string{"gf", "doesnotexist"} {
		if _, msg := e.call(t, sa, "space_members", map[string]any{"space": space}); msg != "not found" {
			t.Errorf("%s: %q", space, msg)
		}
		if _, msg := e.call(t, sa, "space_add_member", map[string]any{"space": space, "user_email": "newbie@x.de", "role": "reader"}); msg != "not found" {
			t.Errorf("add in %s: %q", space, msg)
		}
	}
	// input errors
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"invalid role":  {map[string]any{"space": "support", "user_email": "newbie@x.de", "role": "root"}, "Role"},
		"unknown user":  {map[string]any{"space": "support", "user_email": "ghost@x.de", "role": "reader"}, "unknown"},
		"unknown group": {map[string]any{"space": "support", "group": "Nope", "role": "reader"}, "not found"},
		"both given":    {map[string]any{"space": "support", "user_email": "newbie@x.de", "group": "Vertrieb", "role": "reader"}, "exactly one"},
		"none given":    {map[string]any{"space": "support", "role": "reader"}, "exactly one"},
	} {
		if _, msg := e.call(t, sa, "space_add_member", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}
	// removing needs confirmation
	if _, msg := e.call(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": false}); !strings.Contains(msg, "confirmation") {
		t.Errorf("without confirmation: %q", msg)
	}
	e.mustCall(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": true})
	if out := e.mustCall(t, newbie, "store_knowledge", map[string]any{"title": "T2", "content": "y", "space": "support"}); out["result"] != "choose_space" {
		t.Errorf("removed author can still save: %v", out)
	}
	if _, msg := e.call(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": true}); msg != "not found" {
		t.Errorf("second removal: %q", msg)
	}
	// a regular member has no access to administration
	member := e.session(t, "member@x.de") // curator, but not admin
	if names := toolNames(t, e, member); names["space_members"] {
		t.Error("curator sees space admin tools")
	}
}

func TestInstanceAdminSpaceLifecycle(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	e.session(t, "anna@x.de")
	if _, err := e.users.CreateGroup(e.ctx, "Technik"); err != nil {
		t.Fatal(err)
	}

	out := e.mustCall(t, boss, "admin_create_space", map[string]any{
		"slug": "technik", "name": "Technik", "description": "Docs", "require_approval": true, "default_role": "reader",
		"members": []map[string]any{
			{"user_email": "anna@x.de", "role": "author"},
			{"group": "Technik", "role": "curator"},
			{"user_email": "ghost@x.de", "role": "author"},
		},
	})
	warnings, _ := out["warnings"].([]any)
	if out["slug"] != "technik" || len(warnings) != 1 || !strings.Contains(warnings[0].(string), "ghost@x.de") {
		t.Fatalf("%v", out)
	}
	// default_role=reader: every logged-in user sees the space, Anna may write (with approval)
	anna := e.session(t, "anna@x.de")
	spaces := e.mustCall(t, anna, "list_spaces", nil)["spaces"].([]any)
	var role string
	for _, sp := range spaces {
		if m := sp.(map[string]any); m["slug"] == "technik" {
			role = m["role"].(string)
			if m["requires_approval"] != true {
				t.Error("approval requirement missing")
			}
		}
	}
	if role != "author" {
		t.Errorf("Anna's role: %q", role)
	}
	if got := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "x", "space": "technik"}); got["status"] != "draft" {
		t.Errorf("approval space: %v", got)
	}

	for name, args := range map[string]map[string]any{
		"duplicate slug":       {"slug": "technik", "name": "Again"},
		"invalid slug":         {"slug": "With Spaces", "name": "X"},
		"reserved prefix":      {"slug": "personal-x", "name": "X"},
		"invalid default role": {"slug": "new", "name": "X", "default_role": "admin"},
	} {
		if _, msg := e.call(t, boss, "admin_create_space", args); msg == "" || msg == "internal error" {
			t.Errorf("%s: %q", name, msg)
		}
	}

	// archive: only with confirmation, then invisible, restorable
	if _, msg := e.call(t, boss, "admin_archive_space", map[string]any{"space": "technik"}); msg == "" {
		t.Error("archive without confirm")
	}
	if _, msg := e.call(t, boss, "admin_archive_space", map[string]any{"space": "technik", "confirm": false}); !strings.Contains(msg, "confirmation") {
		t.Errorf("confirm=false: %q", msg)
	}
	e.mustCall(t, boss, "admin_archive_space", map[string]any{"space": "technik", "confirm": true})
	for _, sp := range e.mustCall(t, e.session(t, "anna@x.de"), "list_spaces", nil)["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "technik" {
			t.Error("archived space still visible")
		}
	}
	e.mustCall(t, boss, "admin_restore_space", map[string]any{"space": "technik"}) // instance admin still finds it
	found := false
	for _, sp := range e.mustCall(t, e.session(t, "anna@x.de"), "list_spaces", nil)["spaces"].([]any) {
		found = found || sp.(map[string]any)["slug"] == "technik"
	}
	if !found {
		t.Error("space not visible after restore")
	}
}

func TestDraftOnRequestAndReviewTool(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "recht", "Recht", false, map[string]authz.Role{"cur@x.de": authz.Curator, "reader@x.de": authz.Reader, "author@x.de": authz.Author})
	boss := e.session(t, "boss@x.de")
	cur := e.session(t, "cur@x.de")
	reader := e.session(t, "reader@x.de")
	author := e.session(t, "author@x.de")

	// only curators and admins see the review tool
	for name, cs := range map[string]*mcp.ClientSession{"curator": cur, "instance admin": boss} {
		if !toolNames(t, e, cs)["review_knowledge"] {
			t.Errorf("%s without review_knowledge", name)
		}
	}
	for name, cs := range map[string]*mcp.ClientSession{"reader": reader, "author": author} {
		if toolNames(t, e, cs)["review_knowledge"] {
			t.Errorf("%s sees review_knowledge", name)
		}
	}

	// draft only on request and only in team spaces
	if got := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Direct", "content": "alpha eins", "space": "recht"}); got["status"] != "approved" {
		t.Errorf("without draft: %v", got)
	}
	out := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Contract template", "content": "beta zwei", "space": "recht", "draft": true})
	if out["status"] != "draft" || !strings.Contains(out["message"].(string), "draft") {
		t.Fatalf("with draft: %v", out)
	}
	id := out["id"].(string)
	if _, msg := e.call(t, boss, "store_knowledge", map[string]any{"title": "Private", "content": "gamma drei", "draft": true}); !strings.Contains(msg, "team spaces") {
		t.Errorf("draft in personal space: %q", msg)
	}

	// the curator finds pending drafts via the status filter, the reader sees none
	entries := func(cs *mcp.ClientSession, status string) []any {
		t.Helper()
		return e.mustCall(t, cs, "list_knowledge", map[string]any{"space": "recht", "status": status})["entries"].([]any)
	}
	if l := entries(cur, "draft"); len(l) != 1 || l[0].(map[string]any)["id"] != id {
		t.Errorf("curator list draft: %v", l)
	}
	if l := entries(reader, "draft"); len(l) != 0 {
		t.Errorf("reader list draft: %v", l)
	}
	if _, msg := e.call(t, cur, "list_knowledge", map[string]any{"space": "recht", "status": "broken"}); !strings.Contains(msg, "status") {
		t.Errorf("invalid status: %q", msg)
	}

	// approval: confirmation needed, not for one's own draft, returning only with a note
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "approve"}); !strings.Contains(msg, "confirmation") {
		t.Errorf("approve without confirm: %q", msg)
	}
	curDraft := e.mustCall(t, cur, "store_knowledge", map[string]any{"title": "From the curator", "content": "delta vier", "space": "recht", "draft": true, "force": true})["id"]
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": curDraft, "decision": "approve", "confirm": true}); !strings.Contains(msg, "own draft") {
		t.Errorf("curator approves own draft: %q", msg)
	}
	ownDraft := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Own approval", "content": "epsilon five", "space": "recht", "draft": true, "force": true})["id"]
	if got := e.mustCall(t, boss, "review_knowledge", map[string]any{"id": ownDraft, "decision": "approve", "confirm": true}); got["document"].(map[string]any)["status"] != "approved" {
		t.Errorf("admin approves own draft: %v", got)
	}
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "maybe"}); !strings.Contains(msg, "approve or reject") {
		t.Errorf("unknown decision: %q", msg)
	}
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "reject"}); !strings.Contains(msg, "note") {
		t.Errorf("reject without note: %q", msg)
	}
	rej := e.mustCall(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "reject", "note": "Clause 3 missing"})
	if doc := rej["document"].(map[string]any); doc["status"] != "rejected" || doc["review_note"] != "Clause 3 missing" {
		t.Fatalf("reject: %v", rej)
	}
	if got := e.mustCall(t, boss, "get_knowledge", map[string]any{"id": id}); got["review_note"] != "Clause 3 missing" {
		t.Errorf("author does not see the note: %v", got)
	}
	e.mustCall(t, boss, "update_knowledge", map[string]any{"id": id, "content": "beta two with clause 3"})
	ok := e.mustCall(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "approve", "confirm": true})
	if doc := ok["document"].(map[string]any); doc["status"] != "approved" {
		t.Fatalf("approve: %v", ok)
	}
	if got := e.mustCall(t, reader, "get_knowledge", map[string]any{"id": id}); got["status"] != "approved" {
		t.Errorf("reader after approval: %v", got)
	}
}

func TestInstanceAdminUpdatesSpace(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"anna@x.de": authz.Author, "sa@x.de": authz.Admin})
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	ben := e.session(t, "ben@x.de") // not a member
	sees := func(cs *mcp.ClientSession) map[string]any {
		t.Helper()
		for _, sp := range e.mustCall(t, cs, "list_spaces", nil)["spaces"].([]any) {
			if m := sp.(map[string]any); m["slug"] == "support" {
				return m
			}
		}
		return nil
	}

	before := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Before", "content": "alpha Inhalt", "space": "support"})
	out := e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "require_approval": true})
	if out["requires_approval"] != true || out["name"] != "Support" {
		t.Fatalf("%v", out)
	}
	if got := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "After", "content": "beta Inhalt", "space": "support"}); got["status"] != "draft" {
		t.Errorf("approval requirement does not apply: %v", got)
	}
	if got := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": before["id"]}); got["status"] != "approved" {
		t.Errorf("existing entry lost its status: %v", got)
	}

	// default role opens the space to all logged-in users and closes it again
	e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "default_role": "reader"})
	if m := sees(ben); m == nil || m["role"] != "reader" {
		t.Errorf("default_role=reader: %v", m)
	}
	out = e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "default_role": "none",
		"name": "Kundendienst", "description": "Guides", "confirm": true})
	if out["default_role"] != nil || out["requires_approval"] != true || out["name"] != "Kundendienst" || out["description"] != "Guides" {
		t.Errorf("%v", out)
	}
	if m := sees(ben); m != nil {
		t.Errorf("default_role=none: still visible %v", m)
	}
	if m := sees(anna); m == nil || m["name"] != "Kundendienst" || m["requires_approval"] != true {
		t.Errorf("member sees: %v", m)
	}
	e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "kundendienst", "require_approval": false})
	if m := sees(anna); m["requires_approval"] != false {
		t.Errorf("approval requirement not switched off: %v", m)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":    {map[string]any{"space": "support"}, "Nothing to change"},
		"invalid default role": {map[string]any{"space": "support", "default_role": "admin"}, "default_role"},
		"empty name":           {map[string]any{"space": "support", "name": " "}, "name"},
		"unknown space":        {map[string]any{"space": "doesnotexist", "require_approval": true}, "not found"},
		"personal space":       {map[string]any{"space": "personal", "require_approval": true}, "not found"},
	} {
		if _, msg := e.call(t, boss, "admin_update_space", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}

	var changes []map[string]any
	for _, en := range e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "admin_update_space"})["entries"].([]any) {
		changes = append(changes, en.(map[string]any)["details"].(map[string]any))
	}
	if len(changes) != 4 {
		t.Fatalf("audit entries: %v", changes)
	}
	var sawApproval bool
	for _, d := range changes {
		if _, ok := d["default_role"]; ok && d["require_approval"] != nil {
			t.Errorf("unchanged fields in the audit log: %v", d)
		}
		sawApproval = sawApproval || d["require_approval"] == true
	}
	if !sawApproval {
		t.Errorf("approval requirement missing from the audit log: %v", changes)
	}
}

func TestAdminListUsersShowsMetadataOnly(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Top secret title", "content": "Top secret content alpha"})
	e.mustCall(t, anna, "handoff_save", map[string]any{"title": "Handoff", "content": "Secret handoff"})
	ben := e.session(t, "ben@x.de")
	u, _ := e.users.GetByEmail(e.ctx, "ben@x.de")
	_ = ben
	if err := e.users.SetActive(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}

	res, err := boss.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_list_users"})
	if err != nil || res.IsError {
		t.Fatalf("%v %v", err, res)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "Handoff") {
		t.Errorf("content of personal spaces in the result: %s", raw)
	}
	var out struct {
		StructuredContent struct {
			Users []map[string]any `json:"users"`
		} `json:"structuredContent"`
	}
	_ = json.Unmarshal(raw, &out)
	by := map[string]map[string]any{}
	for _, u := range out.StructuredContent.Users {
		by[u["email"].(string)] = u
	}
	annaInfo := by["anna@x.de"]
	personal, _ := annaInfo["personal_space"].(map[string]any)
	if personal == nil || personal["documents"].(float64) != 1 || personal["bytes"].(float64) < 20 || personal["last_change"] == "" {
		t.Errorf("metadata: %v", annaInfo)
	}
	if by["boss@x.de"]["instance_admin"] != true || by["anna@x.de"]["instance_admin"] != false || by["ben@x.de"]["status"] != "disabled" || annaInfo["last_login"] == nil {
		t.Errorf("status: %v", by)
	}
}

func TestOffboardingConvertsPersonalSpace(t *testing.T) {
	e := setup(t, 0)
	e.srv.set.PersonalRetentionDays = 30
	var revoked []uuid.UUID
	e.srv.RevokeTokens = func(_ context.Context, id uuid.UUID) (int64, error) {
		revoked = append(revoked, id)
		return 2, nil
	}
	e.team(t, "support", "Support", false, nil)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	chef := e.session(t, "chef@x.de")
	ben := e.session(t, "ben@x.de")
	first := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "GEHEIMER-TITEL", "content": "alpha Inhalt"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Notiz", "content": "beta Inhalt"})
	e.mustCall(t, anna, "handoff_save", map[string]any{"title": "Handoff", "content": "State"})

	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"without confirmation": {"admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": false}, "confirmation"},
		"own account":          {"admin_offboard_user", map[string]any{"user_email": "boss@x.de", "confirm": true}, "own"},
		"unknown":              {"admin_offboard_user", map[string]any{"user_email": "ghost@x.de", "confirm": true}, "unknown"},
		"active account":       {"admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "space", "slug": "x1", "name": "X", "confirm": true}, "not locked"},
		"transfer without ok":  {"admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "space", "slug": "x1", "name": "X", "confirm": false}, "confirmation"},
	} {
		if _, msg := e.call(t, boss, tc.tool, tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}

	out := e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	personal, _ := out["personal_space"].(map[string]any)
	if out["revoked_tokens"].(float64) != 2 || out["handoffs_deleted"].(float64) != 1 || personal["documents"].(float64) != 2 ||
		personal["purge_after"] == nil || out["next"] == nil || len(revoked) != 1 {
		t.Fatalf("%v", out)
	}
	if res, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "list_spaces"}); err == nil && !res.IsError {
		t.Error("locked account still has access")
	}
	// offboarding again does not move the deletion deadline
	again := e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	if !strings.Contains(again["message"].(string), "already") || again["personal_space"].(map[string]any)["purge_after"] != personal["purge_after"] {
		t.Errorf("second offboarding: %v", again)
	}

	transfer := func(args map[string]any) (map[string]any, string) {
		args["user_email"], args["confirm"] = "anna@x.de", true
		return e.call(t, boss, "admin_transfer_personal_space", args)
	}
	if _, msg := transfer(map[string]any{"mode": "dance"}); !strings.Contains(msg, "mode") {
		t.Errorf("unknown mode: %q", msg)
	}
	if _, msg := transfer(map[string]any{"mode": "merge", "target_email": "boss@x.de"}); !strings.Contains(msg, "own") {
		t.Errorf("admin takes over the space themselves: %q", msg)
	}
	if _, msg := transfer(map[string]any{"mode": "space", "slug": "support", "name": "X"}); !strings.Contains(msg, "already taken") {
		t.Errorf("slug already taken: %q", msg)
	}
	res, msg := transfer(map[string]any{"mode": "space", "slug": "estate-anna", "name": "Estate Anna",
		"members": []map[string]any{{"user_email": "chef@x.de", "role": "admin"}, {"user_email": "ben@x.de", "role": "curator"}}})
	if msg != "" || res["documents"].(float64) != 2 || res["slug"] != "estate-anna" || res["warnings"] != nil {
		t.Fatalf("%v %q", res, msg)
	}
	if _, msg := transfer(map[string]any{"mode": "space", "slug": "again", "name": "X"}); !strings.Contains(msg, "no longer has a personal space") {
		t.Errorf("second transfer: %q", msg)
	}

	// the supervisor sees both entries, the handoff is deleted
	if list := e.mustCall(t, chef, "list_knowledge", map[string]any{"space": "estate-anna"})["entries"].([]any); len(list) != 2 {
		t.Errorf("entries in the estate: %v", list)
	}
	// the successor takes over an entry into their personal space
	if moved := e.mustCall(t, ben, "move_knowledge", map[string]any{"id": first["id"], "space": "personal", "confirm": true}); moved["result"] != "moved" {
		t.Fatalf("taking over: %v", moved)
	}
	if got := e.mustCall(t, ben, "get_knowledge", map[string]any{"id": first["id"]}); got["content"] != "alpha Inhalt" {
		t.Errorf("entry taken over: %v", got)
	}

	// reactivation: new, empty personal space, the legacy stays with the team
	if got := e.mustCall(t, boss, "admin_reactivate_user", map[string]any{"user_email": "anna@x.de"}); !strings.Contains(got["message"].(string), "new") {
		t.Errorf("reactivation: %v", got)
	}
	anna = e.session(t, "anna@x.de")
	if got, _ := e.mustCall(t, anna, "list_knowledge", nil)["entries"].([]any); len(got) != 0 {
		t.Errorf("new personal space not empty: %v", got)
	}
	for _, sp := range e.mustCall(t, anna, "list_spaces", nil)["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "estate-anna" {
			t.Error("reactivated user sees the estate space")
		}
	}

	actions := map[string]bool{}
	for _, en := range e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "admin_*"})["entries"].([]any) {
		actions[en.(map[string]any)["action"].(string)] = true
	}
	if !actions["admin_offboard_user"] || !actions["admin_transfer_personal_space"] || !actions["admin_reactivate_user"] {
		t.Errorf("audit log: %v", actions)
	}
	raw, _ := json.Marshal(e.mustCall(t, boss, "admin_audit_log", map[string]any{"limit": 200}))
	if strings.Contains(string(raw), "GEHEIMER-TITEL") {
		t.Error("title from the personal space in the audit log")
	}
}

func TestOffboardingMergesIntoSuccessor(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	ben := e.session(t, "ben@x.de")
	e.session(t, "carl@x.de")
	a1 := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Recipe", "content": "alpha Inhalt"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Second", "content": "beta Inhalt"})
	e.mustCall(t, ben, "store_knowledge", map[string]any{"title": "Own", "content": "gamma Inhalt"})
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "carl@x.de", "confirm": true})
	merge := func(target string) (map[string]any, string) {
		return e.call(t, boss, "admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "merge", "target_email": target, "confirm": true})
	}

	if _, msg := merge("carl@x.de"); !strings.Contains(msg, "locked") {
		t.Errorf("locked target: %q", msg)
	}
	if _, msg := merge("ghost@x.de"); !strings.Contains(msg, "unknown") {
		t.Errorf("unknown target: %q", msg)
	}
	// the successor's quota applies; if exceeded, nothing is moved
	u, _ := e.users.GetByEmail(e.ctx, "ben@x.de")
	two := int64(2)
	if _, _, err := e.quota.Set(e.ctx, quota.User, u.ID, quota.Patch{Documents: &two}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if _, msg := merge("ben@x.de"); !strings.Contains(msg, "entries") {
		t.Errorf("quota: %q", msg)
	}
	inherit := int64(-1)
	if _, _, err := e.quota.Set(e.ctx, quota.User, u.ID, quota.Patch{Documents: &inherit}, uuid.Nil); err != nil {
		t.Fatal(err)
	}

	res, msg := merge("ben@x.de")
	if msg != "" || res["documents"].(float64) != 2 {
		t.Fatalf("%v %q", res, msg)
	}
	if list := e.mustCall(t, ben, "list_knowledge", nil)["entries"].([]any); len(list) != 3 {
		t.Errorf("Ben's space: %v", list)
	}
	// Ben now owns the entries like his own, the history continues
	up := e.mustCall(t, ben, "update_knowledge", map[string]any{"id": a1["id"], "title": "Recipe (taken over)"})
	if up["version"].(float64) != 3 || up["space_type"] != "personal" {
		t.Errorf("after takeover: %v", up)
	}
}

func TestPurgeOffboardedPersonalSpaces(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	for _, who := range []string{"anna", "ben", "carl", "dora"} {
		e.mustCall(t, e.session(t, who+"@x.de"), "store_knowledge", map[string]any{"title": "Note " + who, "content": "alpha Inhalt"})
	}
	for _, who := range []string{"anna", "carl", "dora"} {
		e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": who + "@x.de", "confirm": true})
	}
	e.mustCall(t, boss, "admin_transfer_personal_space", map[string]any{"user_email": "carl@x.de", "mode": "space",
		"slug": "estate-carl", "name": "Estate Carl", "confirm": true})
	// Anna and Carl have been gone for a long time, Dora only just now
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE users SET disabled_at = now() - interval '31 days' WHERE email IN ('anna@x.de', 'carl@x.de')`); err != nil {
		t.Fatal(err)
	}

	n, err := e.srv.Docs.PurgeOffboarded(e.ctx, 30)
	if err != nil || n != 1 {
		t.Fatalf("deleted: %d %v", n, err)
	}
	for who, keeps := range map[string]bool{"anna": false, "ben": true, "dora": true} {
		u, _ := e.users.GetByEmail(e.ctx, who+"@x.de")
		if (u.PersonalSpaceID != nil) != keeps {
			t.Errorf("%s: personal space present=%v", who, u.PersonalSpaceID != nil)
		}
	}
	if list := e.mustCall(t, boss, "list_knowledge", map[string]any{"space": "estate-carl"})["entries"].([]any); len(list) != 1 {
		t.Errorf("converted space: %v", list)
	}
	entries := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "purge_personal"})["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("Audit: %v", entries)
	}
	if d := entries[0].(map[string]any)["details"].(map[string]any); d["user"] != "anna@x.de" || d["documents"].(float64) != 1 {
		t.Errorf("audit details: %v", d)
	}
	if n, err := e.srv.Docs.PurgeOffboarded(e.ctx, 30); err != nil || n != 0 {
		t.Errorf("second run: %d %v", n, err)
	}
}

func TestAdminGroups(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	e.session(t, "anna@x.de")
	groups := func(args map[string]any) (map[string]any, string) { return e.call(t, boss, "admin_groups", args) }

	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "create", "name": "Support"})
	if _, msg := groups(map[string]any{"action": "create", "name": "support"}); !strings.Contains(msg, "already exists") {
		t.Errorf("duplicate: %q", msg)
	}
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "add_member", "name": "Support", "user_email": "anna@x.de"})
	if _, msg := groups(map[string]any{"action": "add_member", "name": "Support", "user_email": "ghost@x.de"}); !strings.Contains(msg, "unknown") {
		t.Errorf("unknown user: %q", msg)
	}
	list := e.mustCall(t, boss, "admin_groups", map[string]any{"action": "list"})["groups"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["member_count"].(float64) != 1 {
		t.Errorf("list: %v", list)
	}
	detail := e.mustCall(t, boss, "admin_groups", map[string]any{"action": "list", "name": "support"})
	if m := detail["members"].([]any); len(m) != 1 || m[0] != "anna@x.de" {
		t.Errorf("members: %v", detail)
	}
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "rename", "name": "Support", "new_name": "Kundendienst"})
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "remove_member", "name": "Kundendienst", "user_email": "anna@x.de"})

	// groups of the identity provider are read-only
	if _, err := e.users.CreateGroup(e.ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.Pool.Exec(e.ctx, `INSERT INTO groups (name, source, idp, external_id) VALUES ('Entra-Sales','idp','entra','abc')`); err != nil {
		t.Fatal(err)
	}
	if _, msg := groups(map[string]any{"action": "delete", "name": "Entra-Sales", "confirm": true}); !strings.Contains(msg, "read-only") {
		t.Errorf("delete IdP group: %q", msg)
	}

	if _, msg := groups(map[string]any{"action": "delete", "name": "Kundendienst"}); !strings.Contains(msg, "confirmation") {
		t.Errorf("delete without confirm: %q", msg)
	}
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "delete", "name": "Kundendienst", "confirm": true})
	if _, msg := groups(map[string]any{"action": "delete", "name": "Kundendienst", "confirm": true}); !strings.Contains(msg, "not found") {
		t.Errorf("deleted group: %q", msg)
	}
	if _, msg := groups(map[string]any{"action": "dance"}); !strings.Contains(msg, "action") {
		t.Errorf("unknown action: %q", msg)
	}
}

func TestAuditLogRecordsAdminActionsWithoutPersonalContent(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")

	e.mustCall(t, boss, "admin_create_space", map[string]any{"slug": "technik", "name": "Technik",
		"members": []map[string]any{{"user_email": "anna@x.de", "role": "author"}}})
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "create", "name": "Support"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "PRIVATE-TITLE", "content": "content"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Team knowledge", "content": "beta content", "space": "technik"})
	e.mustCall(t, anna, "search_knowledge", map[string]any{"query": "SECRET-SEARCH-TEXT"})

	log := func(args map[string]any) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, en := range e.mustCall(t, boss, "admin_audit_log", args)["entries"].([]any) {
			out = append(out, en.(map[string]any))
		}
		return out
	}
	admin := log(map[string]any{"action": "admin_*"})
	actions := map[string]bool{}
	for _, en := range admin {
		actions[en["action"].(string)] = true
		if en["user"] != "boss@x.de" || en["client"] != "claude" {
			t.Errorf("entry: %v", en)
		}
	}
	if !actions["admin_create_space"] || !actions["admin_group_create"] || len(admin) != 2 {
		t.Errorf("admin actions: %v", admin)
	}
	if got := log(map[string]any{"action": "space_add_member"}); len(got) != 0 {
		t.Errorf("space_add_member was not triggered: %v", got)
	}

	// team saving with details, personal saving without
	byUser := log(map[string]any{"user": "anna@x.de", "action": "store"})
	if len(byUser) != 2 {
		t.Fatalf("store entries: %v", byUser)
	}
	var personal, team int
	for _, en := range byUser {
		if en["space"] == "personal space" {
			personal++
			if en["details"] != nil {
				t.Errorf("details on personal entry: %v", en)
			}
		} else if en["space"] == "Technik" && en["details"] != nil {
			team++
		}
	}
	if personal != 1 || team != 1 {
		t.Errorf("personal=%d team=%d: %v", personal, team, byUser)
	}
	// space filter
	if got := log(map[string]any{"space": "technik"}); len(got) < 2 {
		t.Errorf("space filter: %v", got)
	}
	// neither title nor search text from a personal scope anywhere in the log
	raw, _ := json.Marshal(log(map[string]any{"limit": 200}))
	for _, secret := range []string{"PRIVATE-TITLE", "SECRET-SEARCH-TEXT"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%q in the audit log", secret)
		}
	}
	// login-like system events and searches can be retrieved
	if got := log(map[string]any{"action": "search"}); len(got) != 1 || got[0]["details"].(map[string]any)["hits"] == nil {
		t.Errorf("search in the log: %v", got)
	}
	// input errors
	for name, args := range map[string]map[string]any{
		"time":     {"since": "yesterday"},
		"document": {"document_id": "x"},
	} {
		if _, msg := e.call(t, boss, "admin_audit_log", args); msg == "" || msg == "internal error" {
			t.Errorf("%s: %q", name, msg)
		}
	}
	if got := log(map[string]any{"since": "2999-01-01"}); len(got) != 0 {
		t.Errorf("Zeitfilter: %d", len(got))
	}
	if _, msg := e.call(t, boss, "admin_audit_log", map[string]any{"space": "doesnotexist"}); msg != "not found" {
		t.Errorf("unknown space: %q", msg)
	}
}

func TestAdminHealth(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	out := e.mustCall(t, boss, "admin_health", nil)
	if out["status"] != "ok" || out["embedding"].(map[string]any)["dimensions"].(float64) != 4 {
		t.Fatalf("%v", out)
	}
	names := map[string]bool{}
	for _, c := range out["checks"].([]any) {
		names[c.(map[string]any)["name"].(string)] = true
	}
	if !names["database"] || !names["tls_certificate"] {
		t.Errorf("checks: %v", names)
	}
}

// Schemas must not contain `"type": [...]`: clients with only one type per schema
// (e.g. Gemini) reject that.
func TestToolSchemasUseSingleTypes(t *testing.T) {
	e := setup(t, 0)
	res, err := e.session(t, "boss@x.de").ListTools(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, isArr := x["type"].([]any); isArr {
				t.Errorf("%s: type is an array", path)
			}
			for k, c := range x {
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(path+"[]", c)
				_ = i
			}
		}
	}
	for _, tool := range res.Tools {
		for kind, schema := range map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema} {
			raw, _ := json.Marshal(schema)
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			walk(tool.Name+"."+kind, v)
		}
	}
}

func TestMoveAndDeleteTools(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "write", "Writable", false, map[string]authz.Role{"anna@x.de": authz.Author, "sadmin@x.de": authz.Admin})
	e.team(t, "read", "Read only", false, map[string]authz.Role{"anna@x.de": authz.Reader})
	anna := e.session(t, "anna@x.de")
	sadmin := e.session(t, "sadmin@x.de")

	out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Note", "content": "alpha content", "space": "write"})
	id := out["id"].(string)

	// target without write permission: nothing is moved, a choice is offered instead of an error
	res := e.mustCall(t, anna, "move_knowledge", map[string]any{"id": id, "space": "read", "confirm": true})
	if res["result"] != "choose_space" {
		t.Fatalf("read-only target: %v", res)
	}
	for _, sp := range res["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "read" {
			t.Errorf("read-only space is offered as a target: %v", res["spaces"])
		}
	}
	// without confirmation nothing is moved
	if _, msg := e.call(t, anna, "move_knowledge", map[string]any{"id": id, "space": "personal", "confirm": false}); !strings.Contains(msg, "confirmation") {
		t.Errorf("move confirm=false: %q", msg)
	}
	res = e.mustCall(t, anna, "move_knowledge", map[string]any{"id": id, "space": "personal", "confirm": true})
	if res["result"] != "moved" || res["document"].(map[string]any)["space_type"] != "personal" {
		t.Fatalf("%v", res)
	}

	// deleting: author may not, space admin does not see the personal entry
	out = e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Team", "content": "beta content", "space": "write"})
	teamID := out["id"].(string)
	if _, msg := e.call(t, anna, "delete_knowledge", map[string]any{"id": teamID, "confirm": true}); !strings.Contains(msg, "permission") {
		t.Errorf("author deletes: %q", msg)
	}
	if _, msg := e.call(t, sadmin, "delete_knowledge", map[string]any{"id": teamID, "confirm": false}); !strings.Contains(msg, "confirmation") {
		t.Errorf("delete confirm=false: %q", msg)
	}
	if _, msg := e.call(t, sadmin, "delete_knowledge", map[string]any{"id": id, "confirm": true}); msg != "not found" {
		t.Errorf("foreign personal entry: %q", msg)
	}
	e.mustCall(t, sadmin, "delete_knowledge", map[string]any{"id": teamID, "confirm": true})
	if _, msg := e.call(t, anna, "get_knowledge", map[string]any{"id": teamID}); msg != "not found" {
		t.Errorf("deleted entry: %q", msg)
	}
}

// An instance admin changes the limits at runtime: for all personal or team spaces, for one user
// or one space, and every change applies to the next write.
func TestAdminQuotas(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{"anna@x.de": authz.Author})
	boss, anna := e.session(t, "boss@x.de"), e.session(t, "anna@x.de")
	store := func(space, title string) string {
		_, msg := e.call(t, anna, "store_knowledge", map[string]any{"title": title, "content": "Inhalt von " + title, "space": space, "force": true})
		return msg
	}

	// the tool does not even exist for users (it is not in their tool list)
	if res, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_quotas", Arguments: map[string]any{"action": "get"}}); err == nil && !res.IsError {
		t.Fatal("a user used admin_quotas")
	}
	// get shows both defaults with their source
	defs := e.mustCall(t, boss, "admin_quotas", map[string]any{"action": "get"})["targets"].([]any)
	if len(defs) != 2 {
		t.Fatalf("defaults: %v", defs)
	}
	personal := defs[0].(map[string]any)["limits"].(map[string]any)["documents"].(map[string]any)
	if personal["limit"] != float64(100) || personal["source"] != "config" {
		t.Fatalf("personal default: %v", personal)
	}

	// invalid and unconfirmed requests change nothing
	set := func(args map[string]any) (map[string]any, string) {
		args["action"] = "set"
		return e.call(t, boss, "admin_quotas", args)
	}
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no values":       {map[string]any{"scope": "personal_default", "confirm": true}, "at least one"},
		"no confirmation": {map[string]any{"scope": "personal_default", "documents": 2}, "confirm"},
		"below -1":        {map[string]any{"scope": "personal_default", "documents": -5, "confirm": true}, "or more"},
		"unknown scope":   {map[string]any{"scope": "planet", "documents": 2, "confirm": true}, "scope must be"},
		"unknown user":    {map[string]any{"scope": "user", "user_email": "ghost@x.de", "documents": 2, "confirm": true}, "unknown"},
		"unknown space":   {map[string]any{"scope": "space", "space": "nowhere", "documents": 2, "confirm": true}, "not found"},
	} {
		if _, msg := set(tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}

	// all personal spaces: two entries, applied at once
	if _, msg := set(map[string]any{"scope": "personal_default", "documents": 2, "confirm": true}); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("my personal space", "eins"); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("my personal space", "zwei"); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("my personal space", "drei"); !strings.Contains(msg, "limit of this space is 2") || !strings.Contains(msg, "instance admin") {
		t.Fatalf("third entry: %q", msg)
	}
	// one user gets more: the user override wins over the default
	if _, msg := set(map[string]any{"scope": "user", "user_email": "anna@x.de", "documents": 5, "confirm": true}); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("my personal space", "drei"); msg != "" {
		t.Fatalf("after the user override: %q", msg)
	}
	got := e.mustCall(t, boss, "admin_quotas", map[string]any{"action": "get", "scope": "user", "user_email": "anna@x.de"})["targets"].([]any)[0].(map[string]any)
	docs := got["limits"].(map[string]any)["documents"].(map[string]any)
	if docs["limit"] != float64(5) || docs["source"] != "user" || docs["used"] != float64(3) || got["overrides"].(map[string]any)["documents"] != float64(5) {
		t.Fatalf("user target: %v", got)
	}
	// -1 removes the override again: the default of 2 applies, and the user is over it
	set(map[string]any{"scope": "user", "user_email": "anna@x.de", "documents": -1, "confirm": true})
	if msg := store("my personal space", "vier"); !strings.Contains(msg, "limit of this space is 2") {
		t.Fatalf("after removing the override: %q", msg)
	}

	// one team space, and all team spaces
	if _, msg := set(map[string]any{"scope": "space", "space": "dev", "documents": 1, "confirm": true}); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("dev", "a"); msg != "" {
		t.Fatal(msg)
	}
	if msg := store("dev", "b"); !strings.Contains(msg, "limit of this space is 1") {
		t.Fatalf("team space: %q", msg)
	}
	set(map[string]any{"scope": "space", "space": "dev", "documents": -1, "confirm": true})
	set(map[string]any{"scope": "team_default", "documents": 50, "skills": 7, "confirm": true})
	if msg := store("dev", "b"); msg != "" {
		t.Fatalf("after raising all team spaces: %q", msg)
	}
	team := e.mustCall(t, boss, "admin_quotas", map[string]any{"action": "get", "scope": "space", "space": "dev"})["targets"].([]any)[0].(map[string]any)["limits"].(map[string]any)
	if d := team["documents"].(map[string]any); d["limit"] != float64(50) || d["source"] != "runtime default" || d["used"] != float64(2) {
		t.Errorf("team documents: %v", d)
	}
	if s := team["skills"].(map[string]any); s["limit"] != float64(7) || s["source"] != "runtime default" {
		t.Errorf("team skills: %v", s)
	}

	// every change is audited with old and new values
	var n int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = 'admin_quota_set' AND details->'changes' IS NOT NULL`).Scan(&n); err != nil || n < 6 {
		t.Errorf("audit entries: %d %v", n, err)
	}
	// space admins see the limits and usage of their space
	e.team(t, "ops", "Operations", false, map[string]authz.Role{"anna@x.de": authz.Admin})
	m := e.mustCall(t, anna, "space_members", map[string]any{"space": "ops"})
	if q, _ := m["quota"].(map[string]any); q == nil || q["documents"] == nil {
		t.Errorf("space_members shows no quota: %v", m)
	}
}

// An instance admin installs a licence at run time; it applies at once and is audited.
func TestAdminLicense(t *testing.T) {
	e := setup(t, 0)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	line := product.CurrentLine()
	e.srv.License = license.NewManager(license.Config{Anchors: license.Anchors{"k1": pub}, Line: line,
		BaseURL: "https://atrium.example.com", Pool: e.srv.Pool, CommunityMaxUsers: 3})
	e.team(t, "dev", "Development", false, map[string]authz.Role{"anna@x.de": authz.Author})
	boss, anna := e.session(t, "boss@x.de"), e.session(t, "anna@x.de")

	mint := func(domain, until string) string {
		tok, err := license.Sign(priv, license.Claims{V: 1, ID: "lic-" + until, Licensee: "Example GmbH", Domain: domain, Edition: "pro",
			Features: []string{"scim"}, Limits: map[string]int64{"users": 50}, Issued: "2026-01-01", MaintenanceUntil: until, IssuerKey: "k1"})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	view := func(m map[string]any) (string, map[string]any) {
		l := m["license"].(map[string]any)
		return l["status"].(string), l
	}
	// seats checks the active accounts and the user limit in force shown by the tool
	seats := func(m map[string]any, limit int, over bool) {
		t.Helper()
		var n int
		if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM users WHERE status = 'active'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		u := m["users"].(map[string]any)
		if u["active"] != float64(n) || u["limit"] != float64(limit) || u["over_limit"] != over {
			t.Errorf("users: %v, want %d active, limit %d, over %v", u, n, limit, over)
		}
		if w, _ := m["warning"].(string); (w != "") != (n >= limit) {
			t.Errorf("warning with %d active and limit %d: %q", n, limit, w)
		}
	}

	// users do not even see the tool; server_info shows only the edition
	if res, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_license", Arguments: map[string]any{"action": "get"}}); err == nil && !res.IsError {
		t.Fatal("a user used admin_license")
	}
	if got := e.mustCall(t, anna, "server_info", map[string]any{}); got["edition"] != "community" || got["line"] != line.Name {
		t.Errorf("server_info: %v", got)
	}

	out := e.mustCall(t, boss, "admin_license", map[string]any{"action": "get"})
	if st, _ := view(out); st != "none" {
		t.Fatalf("fresh instance: %s", st)
	}
	seats(out, 3, false)
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no licence":      {map[string]any{"action": "install", "confirm": true}, "licence text"},
		"no confirmation": {map[string]any{"action": "install", "license": mint("atrium.example.com", "2099-12-31")}, "confirm"},
		"garbage":         {map[string]any{"action": "install", "license": "atrium-license-v1.x.y", "confirm": true}, "not accepted"},
		"other domain":    {map[string]any{"action": "install", "license": mint("other.example.com", "2099-12-31"), "confirm": true}, "other.example.com"},
		"unknown action":  {map[string]any{"action": "steal"}, "action must be"},
	} {
		if _, msg := e.call(t, boss, "admin_license", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}

	out = e.mustCall(t, boss, "admin_license", map[string]any{"action": "install", "license": mint("Atrium.Example.com", "2099-12-31"), "confirm": true})
	st, l := view(out)
	if st != "valid" || l["edition"] != "pro" || l["licensee"] != "Example GmbH" || l["source"] != "installed" || l["maintenance_until"] != "2099-12-31" {
		t.Fatalf("install: %v", out)
	}
	if lim := l["limits"].(map[string]any); lim["users"] != float64(50) {
		t.Errorf("limits: %v", l["limits"])
	}
	// accounts created under the licence go beyond the Community limit
	for _, email := range []string{"carl@x.de", "dora@x.de", "emil@x.de"} {
		if _, err := e.users.Provision(e.ctx, users.Identity{EmailVerified: true, IDP: "t", Subject: email, Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	seats(e.mustCall(t, boss, "admin_license", map[string]any{"action": "get"}), 50, false)
	if got := e.mustCall(t, anna, "server_info", map[string]any{}); got["edition"] != "pro" {
		t.Errorf("server_info after install: %v", got)
	}
	// a licence that does not cover this build is accepted and says why
	out = e.mustCall(t, boss, "admin_license", map[string]any{"action": "install", "license": mint("atrium.example.com", "2020-01-01"), "confirm": true})
	if st, l := view(out); st != "not covered" || !strings.Contains(l["detail"].(string), "2020-01-01") {
		t.Fatalf("not covering: %v", out)
	}
	seats(out, 3, true)
	// the question before a removal names what it means for the accounts
	if _, msg := e.call(t, boss, "admin_license", map[string]any{"action": "remove"}); !strings.Contains(msg, "confirm") || !strings.Contains(msg, "Community allows 3") {
		t.Errorf("remove without confirmation: %q", msg)
	}
	out = e.mustCall(t, boss, "admin_license", map[string]any{"action": "remove", "confirm": true})
	seats(out, 3, true)
	if w, _ := out["warning"].(string); !strings.Contains(w, "the Community edition allows 3") || !strings.Contains(w, "keep working") {
		t.Errorf("warning after remove: %q", w)
	}
	if st, _ := view(e.mustCall(t, boss, "admin_license", map[string]any{"action": "get"})); st != "none" {
		t.Errorf("after remove: %s", st)
	}
	// the audit log has the changes, the licence text itself is not in it
	var n int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action IN ('license_install', 'license_remove')`).Scan(&n); err != nil || n != 3 {
		t.Errorf("audit entries: %d %v", n, err)
	}
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE details::text LIKE '%atrium-license-v1%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the licence text is in the audit log: %d %v", n, err)
	}
}
