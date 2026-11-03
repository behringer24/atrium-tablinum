package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
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
		t.Errorf("normaler Nutzer: %v", plain)
	}
	if !curator["review_knowledge"] || curator["space_members"] || curator["admin_create_space"] {
		t.Errorf("Kurator: %v", curator)
	}
	if !sadmin["review_knowledge"] || !boss["review_knowledge"] {
		t.Error("Bereichs-Admin und Instanz-Admin ohne review_knowledge")
	}
	// Admin of one's own personal space is not enough for space admin tools (see 'plain')
	if !sadmin["space_members"] || !sadmin["space_add_member"] || !sadmin["space_remove_member"] || sadmin["admin_create_space"] || sadmin["admin_update_space"] || sadmin["admin_audit_log"] {
		t.Errorf("Bereichs-Admin: %v", sadmin)
	}
	for _, name := range []string{"admin_create_space", "admin_update_space", "admin_archive_space", "admin_restore_space", "admin_list_users",
		"admin_offboard_user", "admin_transfer_personal_space", "admin_reactivate_user", "admin_groups", "admin_audit_log", "admin_health", "space_members"} {
		if !boss[name] {
			t.Errorf("Instanz-Admin ohne %s", name)
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
		t.Fatal("Admin-Tools fehlen nach Ernennung")
	}
	e.mustCall(t, cs, "admin_health", nil)
	if err := e.users.SetInstanceAdmin(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	// The endpoint is stateless: the tool list is built on every call from the current
	// permissions, so a revoked admin tool is gone immediately.
	if _, err := cs.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_health"}); err == nil {
		t.Error("entzogene Admin-Rechte: Werkzeug noch aufrufbar")
	}
	// The permission check in the tool itself remains as a second safeguard.
	boss, err := e.users.GetByEmail(e.ctx, "anna@x.de")
	if err != nil {
		t.Fatal(err)
	}
	if e.srv.allowed(e.ctx, tierInstanceAdmin, docs.Actor{User: boss}) {
		t.Error("allowed() lässt Nicht-Admin zu")
	}
	// A tool the session never had does not exist for the client
	if _, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_health"}); err == nil {
		t.Error("nicht registriertes Tool aufrufbar")
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
		t.Errorf("Mitglieder: %v", byName)
	}
	// the newly added author can now save there
	newbie := e.session(t, "newbie@x.de")
	if out := e.mustCall(t, newbie, "store_knowledge", map[string]any{"title": "T", "content": "x", "space": "support"}); out["result"] != "stored" {
		t.Errorf("neue Autorin: %v", out)
	}

	// foreign space: same answer as for a nonexistent one
	for _, space := range []string{"gf", "gibtsnicht"} {
		if _, msg := e.call(t, sa, "space_members", map[string]any{"space": space}); msg != "nicht gefunden" {
			t.Errorf("%s: %q", space, msg)
		}
		if _, msg := e.call(t, sa, "space_add_member", map[string]any{"space": space, "user_email": "newbie@x.de", "role": "reader"}); msg != "nicht gefunden" {
			t.Errorf("add in %s: %q", space, msg)
		}
	}
	// input errors
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"ungültige Rolle":    {map[string]any{"space": "support", "user_email": "newbie@x.de", "role": "root"}, "Rolle"},
		"unbekannter Nutzer": {map[string]any{"space": "support", "user_email": "ghost@x.de", "role": "reader"}, "unbekannt"},
		"unbekannte Gruppe":  {map[string]any{"space": "support", "group": "Nope", "role": "reader"}, "nicht gefunden"},
		"beides angegeben":   {map[string]any{"space": "support", "user_email": "newbie@x.de", "group": "Vertrieb", "role": "reader"}, "genau eines"},
		"keines angegeben":   {map[string]any{"space": "support", "role": "reader"}, "genau eines"},
	} {
		if _, msg := e.call(t, sa, "space_add_member", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q", name, msg)
		}
	}
	// removing needs confirmation
	if _, msg := e.call(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": false}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("ohne Bestätigung: %q", msg)
	}
	e.mustCall(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": true})
	if out := e.mustCall(t, newbie, "store_knowledge", map[string]any{"title": "T2", "content": "y", "space": "support"}); out["result"] != "choose_space" {
		t.Errorf("entfernte Autorin kann noch speichern: %v", out)
	}
	if _, msg := e.call(t, sa, "space_remove_member", map[string]any{"space": "support", "user_email": "newbie@x.de", "confirm": true}); msg != "nicht gefunden" {
		t.Errorf("zweites Entfernen: %q", msg)
	}
	// a regular member has no access to administration
	member := e.session(t, "member@x.de") // curator, but not admin
	if names := toolNames(t, e, member); names["space_members"] {
		t.Error("Kurator sieht Bereichs-Admin-Tools")
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
		"slug": "technik", "name": "Technik", "description": "Doku", "require_approval": true, "default_role": "reader",
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
				t.Error("Freigabepflicht fehlt")
			}
		}
	}
	if role != "author" {
		t.Errorf("Annas Rolle: %q", role)
	}
	if got := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "x", "space": "technik"}); got["status"] != "draft" {
		t.Errorf("Freigabe-Bereich: %v", got)
	}

	for name, args := range map[string]map[string]any{
		"doppeltes Kürzel":        {"slug": "technik", "name": "Nochmal"},
		"ungültiges Kürzel":       {"slug": "Mit Leerzeichen", "name": "X"},
		"reserviertes Präfix":     {"slug": "personal-x", "name": "X"},
		"ungültige Standardrolle": {"slug": "neu", "name": "X", "default_role": "admin"},
	} {
		if _, msg := e.call(t, boss, "admin_create_space", args); msg == "" || msg == "interner Fehler" {
			t.Errorf("%s: %q", name, msg)
		}
	}

	// archive: only with confirmation, then invisible, restorable
	if _, msg := e.call(t, boss, "admin_archive_space", map[string]any{"space": "technik"}); msg == "" {
		t.Error("archivieren ohne confirm")
	}
	if _, msg := e.call(t, boss, "admin_archive_space", map[string]any{"space": "technik", "confirm": false}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("confirm=false: %q", msg)
	}
	e.mustCall(t, boss, "admin_archive_space", map[string]any{"space": "technik", "confirm": true})
	for _, sp := range e.mustCall(t, e.session(t, "anna@x.de"), "list_spaces", nil)["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "technik" {
			t.Error("archivierter Bereich noch sichtbar")
		}
	}
	e.mustCall(t, boss, "admin_restore_space", map[string]any{"space": "technik"}) // instance admin still finds it
	found := false
	for _, sp := range e.mustCall(t, e.session(t, "anna@x.de"), "list_spaces", nil)["spaces"].([]any) {
		found = found || sp.(map[string]any)["slug"] == "technik"
	}
	if !found {
		t.Error("Bereich nach Wiederherstellung nicht sichtbar")
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
	for name, cs := range map[string]*mcp.ClientSession{"Kurator": cur, "Instanz-Admin": boss} {
		if !toolNames(t, e, cs)["review_knowledge"] {
			t.Errorf("%s ohne review_knowledge", name)
		}
	}
	for name, cs := range map[string]*mcp.ClientSession{"Leser": reader, "Autor": author} {
		if toolNames(t, e, cs)["review_knowledge"] {
			t.Errorf("%s sieht review_knowledge", name)
		}
	}

	// draft only on request and only in team spaces
	if got := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Direkt", "content": "alpha eins", "space": "recht"}); got["status"] != "approved" {
		t.Errorf("ohne draft: %v", got)
	}
	out := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Vertragsmuster", "content": "beta zwei", "space": "recht", "draft": true})
	if out["status"] != "draft" || !strings.Contains(out["message"].(string), "Entwurf") {
		t.Fatalf("mit draft: %v", out)
	}
	id := out["id"].(string)
	if _, msg := e.call(t, boss, "store_knowledge", map[string]any{"title": "Privat", "content": "gamma drei", "draft": true}); !strings.Contains(msg, "team-bereichen") {
		t.Errorf("draft im persönlichen Bereich: %q", msg)
	}

	// the curator finds pending drafts via the status filter, the reader sees none
	entries := func(cs *mcp.ClientSession, status string) []any {
		t.Helper()
		return e.mustCall(t, cs, "list_knowledge", map[string]any{"space": "recht", "status": status})["entries"].([]any)
	}
	if l := entries(cur, "draft"); len(l) != 1 || l[0].(map[string]any)["id"] != id {
		t.Errorf("Kurator-Liste draft: %v", l)
	}
	if l := entries(reader, "draft"); len(l) != 0 {
		t.Errorf("Leser-Liste draft: %v", l)
	}
	if _, msg := e.call(t, cur, "list_knowledge", map[string]any{"space": "recht", "status": "kaputt"}); !strings.Contains(msg, "status") {
		t.Errorf("ungültiger Status: %q", msg)
	}

	// approval: confirmation needed, not for one's own draft, returning only with a note
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "approve"}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("approve ohne confirm: %q", msg)
	}
	curDraft := e.mustCall(t, cur, "store_knowledge", map[string]any{"title": "Vom Kurator", "content": "delta vier", "space": "recht", "draft": true, "force": true})["id"]
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": curDraft, "decision": "approve", "confirm": true}); !strings.Contains(msg, "eigenen entwurf") {
		t.Errorf("Kurator gibt eigenen Entwurf frei: %q", msg)
	}
	ownDraft := e.mustCall(t, boss, "store_knowledge", map[string]any{"title": "Eigene Freigabe", "content": "epsilon fünf", "space": "recht", "draft": true, "force": true})["id"]
	if got := e.mustCall(t, boss, "review_knowledge", map[string]any{"id": ownDraft, "decision": "approve", "confirm": true}); got["document"].(map[string]any)["status"] != "approved" {
		t.Errorf("Admin gibt eigenen Entwurf frei: %v", got)
	}
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "vielleicht"}); !strings.Contains(msg, "approve oder reject") {
		t.Errorf("unbekannte decision: %q", msg)
	}
	if _, msg := e.call(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "reject"}); !strings.Contains(msg, "anmerkung") {
		t.Errorf("reject ohne Anmerkung: %q", msg)
	}
	rej := e.mustCall(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "reject", "note": "Klausel 3 fehlt"})
	if doc := rej["document"].(map[string]any); doc["status"] != "rejected" || doc["review_note"] != "Klausel 3 fehlt" {
		t.Fatalf("reject: %v", rej)
	}
	if got := e.mustCall(t, boss, "get_knowledge", map[string]any{"id": id}); got["review_note"] != "Klausel 3 fehlt" {
		t.Errorf("Autor sieht Anmerkung nicht: %v", got)
	}
	e.mustCall(t, boss, "update_knowledge", map[string]any{"id": id, "content": "beta zwei mit Klausel 3"})
	ok := e.mustCall(t, cur, "review_knowledge", map[string]any{"id": id, "decision": "approve", "confirm": true})
	if doc := ok["document"].(map[string]any); doc["status"] != "approved" {
		t.Fatalf("approve: %v", ok)
	}
	if got := e.mustCall(t, reader, "get_knowledge", map[string]any{"id": id}); got["status"] != "approved" {
		t.Errorf("Leser nach Freigabe: %v", got)
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

	before := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Vorher", "content": "alpha Inhalt", "space": "support"})
	out := e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "require_approval": true})
	if out["requires_approval"] != true || out["name"] != "Support" {
		t.Fatalf("%v", out)
	}
	if got := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Nachher", "content": "beta Inhalt", "space": "support"}); got["status"] != "draft" {
		t.Errorf("Freigabepflicht greift nicht: %v", got)
	}
	if got := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": before["id"]}); got["status"] != "approved" {
		t.Errorf("bestehender Eintrag hat seinen Status verloren: %v", got)
	}

	// default role opens the space to all logged-in users and closes it again
	e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "default_role": "reader"})
	if m := sees(ben); m == nil || m["role"] != "reader" {
		t.Errorf("default_role=reader: %v", m)
	}
	out = e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "support", "default_role": "none",
		"name": "Kundendienst", "description": "Anleitungen", "confirm": true})
	if out["default_role"] != nil || out["requires_approval"] != true || out["name"] != "Kundendienst" || out["description"] != "Anleitungen" {
		t.Errorf("%v", out)
	}
	if m := sees(ben); m != nil {
		t.Errorf("default_role=none: noch sichtbar %v", m)
	}
	if m := sees(anna); m == nil || m["name"] != "Kundendienst" || m["requires_approval"] != true {
		t.Errorf("Mitglied sieht: %v", m)
	}
	e.mustCall(t, boss, "admin_update_space", map[string]any{"space": "kundendienst", "require_approval": false})
	if m := sees(anna); m["requires_approval"] != false {
		t.Errorf("Freigabepflicht nicht abgeschaltet: %v", m)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nichts zu ändern":        {map[string]any{"space": "support"}, "Nichts zu ändern"},
		"ungültige Standardrolle": {map[string]any{"space": "support", "default_role": "admin"}, "default_role"},
		"leerer Name":             {map[string]any{"space": "support", "name": " "}, "name"},
		"unbekannter Bereich":     {map[string]any{"space": "gibtsnicht", "require_approval": true}, "nicht gefunden"},
		"persönlicher Bereich":    {map[string]any{"space": "personal", "require_approval": true}, "nicht gefunden"},
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
		t.Fatalf("Audit-Einträge: %v", changes)
	}
	var sawApproval bool
	for _, d := range changes {
		if _, ok := d["default_role"]; ok && d["require_approval"] != nil {
			t.Errorf("unveränderte Felder im Audit-Log: %v", d)
		}
		sawApproval = sawApproval || d["require_approval"] == true
	}
	if !sawApproval {
		t.Errorf("Freigabepflicht fehlt im Audit-Log: %v", changes)
	}
}

func TestAdminListUsersShowsMetadataOnly(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Streng geheimer Titel", "content": "Streng geheimer Inhalt alpha"})
	e.mustCall(t, anna, "handoff_save", map[string]any{"title": "Übergabe", "content": "Geheime Übergabe"})
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
	if strings.Contains(string(raw), "geheim") || strings.Contains(string(raw), "Übergabe") {
		t.Errorf("Inhalte persönlicher Bereiche im Ergebnis: %s", raw)
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
		t.Errorf("Metadaten: %v", annaInfo)
	}
	if by["boss@x.de"]["instance_admin"] != true || by["anna@x.de"]["instance_admin"] != false || by["ben@x.de"]["status"] != "disabled" || annaInfo["last_login"] == nil {
		t.Errorf("Status: %v", by)
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
	e.mustCall(t, anna, "handoff_save", map[string]any{"title": "Übergabe", "content": "Stand"})

	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"ohne Bestätigung":   {"admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": false}, "Bestätigung"},
		"eigenes Konto":      {"admin_offboard_user", map[string]any{"user_email": "boss@x.de", "confirm": true}, "eigene"},
		"unbekannt":          {"admin_offboard_user", map[string]any{"user_email": "ghost@x.de", "confirm": true}, "unbekannt"},
		"aktives Konto":      {"admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "space", "slug": "x1", "name": "X", "confirm": true}, "nicht gesperrt"},
		"Übertragen ohne ok": {"admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "space", "slug": "x1", "name": "X", "confirm": false}, "Bestätigung"},
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
		t.Error("gesperrtes Konto hat noch Zugriff")
	}
	// offboarding again does not move the deletion deadline
	again := e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	if !strings.Contains(again["message"].(string), "bereits") || again["personal_space"].(map[string]any)["purge_after"] != personal["purge_after"] {
		t.Errorf("zweites Offboarding: %v", again)
	}

	transfer := func(args map[string]any) (map[string]any, string) {
		args["user_email"], args["confirm"] = "anna@x.de", true
		return e.call(t, boss, "admin_transfer_personal_space", args)
	}
	if _, msg := transfer(map[string]any{"mode": "dance"}); !strings.Contains(msg, "mode") {
		t.Errorf("unbekannter mode: %q", msg)
	}
	if _, msg := transfer(map[string]any{"mode": "merge", "target_email": "boss@x.de"}); !strings.Contains(msg, "eigenen") {
		t.Errorf("Admin übernimmt den Bereich selbst: %q", msg)
	}
	if _, msg := transfer(map[string]any{"mode": "space", "slug": "support", "name": "X"}); !strings.Contains(msg, "vergeben") {
		t.Errorf("vergebenes Kürzel: %q", msg)
	}
	res, msg := transfer(map[string]any{"mode": "space", "slug": "nachlass-anna", "name": "Nachlass Anna",
		"members": []map[string]any{{"user_email": "chef@x.de", "role": "admin"}, {"user_email": "ben@x.de", "role": "curator"}}})
	if msg != "" || res["documents"].(float64) != 2 || res["slug"] != "nachlass-anna" || res["warnings"] != nil {
		t.Fatalf("%v %q", res, msg)
	}
	if _, msg := transfer(map[string]any{"mode": "space", "slug": "nochmal", "name": "X"}); !strings.Contains(msg, "keinen persönlichen Bereich") {
		t.Errorf("zweite Übertragung: %q", msg)
	}

	// the supervisor sees both entries, the handoff is deleted
	if list := e.mustCall(t, chef, "list_knowledge", map[string]any{"space": "nachlass-anna"})["entries"].([]any); len(list) != 2 {
		t.Errorf("Einträge im Nachlass: %v", list)
	}
	// the successor takes over an entry into their personal space
	if moved := e.mustCall(t, ben, "move_knowledge", map[string]any{"id": first["id"], "space": "personal", "confirm": true}); moved["result"] != "moved" {
		t.Fatalf("übernehmen: %v", moved)
	}
	if got := e.mustCall(t, ben, "get_knowledge", map[string]any{"id": first["id"]}); got["content"] != "alpha Inhalt" {
		t.Errorf("übernommener Eintrag: %v", got)
	}

	// reactivation: new, empty personal space, the legacy stays with the team
	if got := e.mustCall(t, boss, "admin_reactivate_user", map[string]any{"user_email": "anna@x.de"}); !strings.Contains(got["message"].(string), "neuer") {
		t.Errorf("Reaktivierung: %v", got)
	}
	anna = e.session(t, "anna@x.de")
	if got, _ := e.mustCall(t, anna, "list_knowledge", nil)["entries"].([]any); len(got) != 0 {
		t.Errorf("neuer persönlicher Bereich nicht leer: %v", got)
	}
	for _, sp := range e.mustCall(t, anna, "list_spaces", nil)["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "nachlass-anna" {
			t.Error("Reaktivierte sieht den Nachlass-Bereich")
		}
	}

	actions := map[string]bool{}
	for _, en := range e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "admin_*"})["entries"].([]any) {
		actions[en.(map[string]any)["action"].(string)] = true
	}
	if !actions["admin_offboard_user"] || !actions["admin_transfer_personal_space"] || !actions["admin_reactivate_user"] {
		t.Errorf("Audit-Log: %v", actions)
	}
	raw, _ := json.Marshal(e.mustCall(t, boss, "admin_audit_log", map[string]any{"limit": 200}))
	if strings.Contains(string(raw), "GEHEIMER-TITEL") {
		t.Error("Titel aus dem persönlichen Bereich im Audit-Log")
	}
}

func TestOffboardingMergesIntoSuccessor(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	ben := e.session(t, "ben@x.de")
	e.session(t, "carl@x.de")
	a1 := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Rezept", "content": "alpha Inhalt"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Zweites", "content": "beta Inhalt"})
	e.mustCall(t, ben, "store_knowledge", map[string]any{"title": "Eigenes", "content": "gamma Inhalt"})
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "carl@x.de", "confirm": true})
	merge := func(target string) (map[string]any, string) {
		return e.call(t, boss, "admin_transfer_personal_space", map[string]any{"user_email": "anna@x.de", "mode": "merge", "target_email": target, "confirm": true})
	}

	if _, msg := merge("carl@x.de"); !strings.Contains(msg, "gesperrt") {
		t.Errorf("gesperrtes Ziel: %q", msg)
	}
	if _, msg := merge("ghost@x.de"); !strings.Contains(msg, "unbekannt") {
		t.Errorf("unbekanntes Ziel: %q", msg)
	}
	// the successor's quota applies; if exceeded, nothing is moved
	u, _ := e.users.GetByEmail(e.ctx, "ben@x.de")
	two := 2
	if err := e.users.SetQuota(e.ctx, u.ID, &two, nil); err != nil {
		t.Fatal(err)
	}
	if _, msg := merge("ben@x.de"); !strings.Contains(msg, "einträge") {
		t.Errorf("Kontingent: %q", msg)
	}
	if err := e.users.SetQuota(e.ctx, u.ID, nil, nil); err != nil {
		t.Fatal(err)
	}

	res, msg := merge("ben@x.de")
	if msg != "" || res["documents"].(float64) != 2 {
		t.Fatalf("%v %q", res, msg)
	}
	if list := e.mustCall(t, ben, "list_knowledge", nil)["entries"].([]any); len(list) != 3 {
		t.Errorf("Bens Bereich: %v", list)
	}
	// Ben now owns the entries like his own, the history continues
	up := e.mustCall(t, ben, "update_knowledge", map[string]any{"id": a1["id"], "title": "Rezept (übernommen)"})
	if up["version"].(float64) != 3 || up["space_type"] != "personal" {
		t.Errorf("nach Übernahme: %v", up)
	}
}

func TestPurgeOffboardedPersonalSpaces(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	for _, who := range []string{"anna", "ben", "carl", "dora"} {
		e.mustCall(t, e.session(t, who+"@x.de"), "store_knowledge", map[string]any{"title": "Notiz " + who, "content": "alpha Inhalt"})
	}
	for _, who := range []string{"anna", "carl", "dora"} {
		e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": who + "@x.de", "confirm": true})
	}
	e.mustCall(t, boss, "admin_transfer_personal_space", map[string]any{"user_email": "carl@x.de", "mode": "space",
		"slug": "nachlass-carl", "name": "Nachlass Carl", "confirm": true})
	// Anna and Carl have been gone for a long time, Dora only just now
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE users SET disabled_at = now() - interval '31 days' WHERE email IN ('anna@x.de', 'carl@x.de')`); err != nil {
		t.Fatal(err)
	}

	n, err := e.srv.Docs.PurgeOffboarded(e.ctx, 30)
	if err != nil || n != 1 {
		t.Fatalf("gelöscht: %d %v", n, err)
	}
	for who, keeps := range map[string]bool{"anna": false, "ben": true, "dora": true} {
		u, _ := e.users.GetByEmail(e.ctx, who+"@x.de")
		if (u.PersonalSpaceID != nil) != keeps {
			t.Errorf("%s: persönlicher Bereich vorhanden=%v", who, u.PersonalSpaceID != nil)
		}
	}
	if list := e.mustCall(t, boss, "list_knowledge", map[string]any{"space": "nachlass-carl"})["entries"].([]any); len(list) != 1 {
		t.Errorf("umgewandelter Bereich: %v", list)
	}
	entries := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": "purge_personal"})["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("Audit: %v", entries)
	}
	if d := entries[0].(map[string]any)["details"].(map[string]any); d["user"] != "anna@x.de" || d["documents"].(float64) != 1 {
		t.Errorf("Audit-Details: %v", d)
	}
	if n, err := e.srv.Docs.PurgeOffboarded(e.ctx, 30); err != nil || n != 0 {
		t.Errorf("zweiter Lauf: %d %v", n, err)
	}
}

func TestAdminGroups(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	e.session(t, "anna@x.de")
	groups := func(args map[string]any) (map[string]any, string) { return e.call(t, boss, "admin_groups", args) }

	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "create", "name": "Support"})
	if _, msg := groups(map[string]any{"action": "create", "name": "support"}); !strings.Contains(msg, "existiert") {
		t.Errorf("Duplikat: %q", msg)
	}
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "add_member", "name": "Support", "user_email": "anna@x.de"})
	if _, msg := groups(map[string]any{"action": "add_member", "name": "Support", "user_email": "ghost@x.de"}); !strings.Contains(msg, "unbekannt") {
		t.Errorf("unbekannter Nutzer: %q", msg)
	}
	list := e.mustCall(t, boss, "admin_groups", map[string]any{"action": "list"})["groups"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["member_count"].(float64) != 1 {
		t.Errorf("Liste: %v", list)
	}
	detail := e.mustCall(t, boss, "admin_groups", map[string]any{"action": "list", "name": "support"})
	if m := detail["members"].([]any); len(m) != 1 || m[0] != "anna@x.de" {
		t.Errorf("Mitglieder: %v", detail)
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
	if _, msg := groups(map[string]any{"action": "delete", "name": "Entra-Sales", "confirm": true}); !strings.Contains(msg, "schreibgeschützt") {
		t.Errorf("IdP-Gruppe löschen: %q", msg)
	}

	if _, msg := groups(map[string]any{"action": "delete", "name": "Kundendienst"}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("Löschen ohne confirm: %q", msg)
	}
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "delete", "name": "Kundendienst", "confirm": true})
	if _, msg := groups(map[string]any{"action": "delete", "name": "Kundendienst", "confirm": true}); !strings.Contains(msg, "nicht gefunden") {
		t.Errorf("gelöschte Gruppe: %q", msg)
	}
	if _, msg := groups(map[string]any{"action": "dance"}); !strings.Contains(msg, "action") {
		t.Errorf("unbekannte Aktion: %q", msg)
	}
}

func TestAuditLogRecordsAdminActionsWithoutPersonalContent(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")

	e.mustCall(t, boss, "admin_create_space", map[string]any{"slug": "technik", "name": "Technik",
		"members": []map[string]any{{"user_email": "anna@x.de", "role": "author"}}})
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "create", "name": "Support"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "PRIVATER-TITEL", "content": "inhalt"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Teamwissen", "content": "beta inhalt", "space": "technik"})
	e.mustCall(t, anna, "search_knowledge", map[string]any{"query": "GEHEIMER-SUCHTEXT"})

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
			t.Errorf("Eintrag: %v", en)
		}
	}
	if !actions["admin_create_space"] || !actions["admin_group_create"] || len(admin) != 2 {
		t.Errorf("Admin-Aktionen: %v", admin)
	}
	if got := log(map[string]any{"action": "space_add_member"}); len(got) != 0 {
		t.Errorf("space_add_member wurde nicht ausgelöst: %v", got)
	}

	// team saving with details, personal saving without
	byUser := log(map[string]any{"user": "anna@x.de", "action": "store"})
	if len(byUser) != 2 {
		t.Fatalf("store-Einträge: %v", byUser)
	}
	var personal, team int
	for _, en := range byUser {
		if en["space"] == "persönlicher Bereich" {
			personal++
			if en["details"] != nil {
				t.Errorf("Details bei persönlichem Eintrag: %v", en)
			}
		} else if en["space"] == "Technik" && en["details"] != nil {
			team++
		}
	}
	if personal != 1 || team != 1 {
		t.Errorf("persönlich=%d team=%d: %v", personal, team, byUser)
	}
	// space filter
	if got := log(map[string]any{"space": "technik"}); len(got) < 2 {
		t.Errorf("Bereichsfilter: %v", got)
	}
	// neither title nor search text from a personal scope anywhere in the log
	raw, _ := json.Marshal(log(map[string]any{"limit": 200}))
	for _, secret := range []string{"PRIVATER-TITEL", "GEHEIMER-SUCHTEXT"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%q im Audit-Log", secret)
		}
	}
	// login-like system events and searches can be retrieved
	if got := log(map[string]any{"action": "search"}); len(got) != 1 || got[0]["details"].(map[string]any)["hits"] == nil {
		t.Errorf("Suche im Log: %v", got)
	}
	// input errors
	for name, args := range map[string]map[string]any{
		"Zeit":     {"since": "gestern"},
		"Dokument": {"document_id": "x"},
	} {
		if _, msg := e.call(t, boss, "admin_audit_log", args); msg == "" || msg == "interner Fehler" {
			t.Errorf("%s: %q", name, msg)
		}
	}
	if got := log(map[string]any{"since": "2999-01-01"}); len(got) != 0 {
		t.Errorf("Zeitfilter: %d", len(got))
	}
	if _, msg := e.call(t, boss, "admin_audit_log", map[string]any{"space": "gibtsnicht"}); msg != "nicht gefunden" {
		t.Errorf("unbekannter Bereich: %q", msg)
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
		t.Errorf("Prüfungen: %v", names)
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
				t.Errorf("%s: type ist ein Array", path)
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
	e.team(t, "write", "Schreibbar", false, map[string]authz.Role{"anna@x.de": authz.Author, "sadmin@x.de": authz.Admin})
	e.team(t, "read", "Nur lesbar", false, map[string]authz.Role{"anna@x.de": authz.Reader})
	anna := e.session(t, "anna@x.de")
	sadmin := e.session(t, "sadmin@x.de")

	out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Notiz", "content": "alpha Inhalt", "space": "write"})
	id := out["id"].(string)

	// target without write permission: nothing is moved, a choice is offered instead of an error
	res := e.mustCall(t, anna, "move_knowledge", map[string]any{"id": id, "space": "read", "confirm": true})
	if res["result"] != "choose_space" {
		t.Fatalf("nur lesbares Ziel: %v", res)
	}
	for _, sp := range res["spaces"].([]any) {
		if sp.(map[string]any)["slug"] == "read" {
			t.Errorf("nur lesbarer Bereich wird als Ziel angeboten: %v", res["spaces"])
		}
	}
	// without confirmation nothing is moved
	if _, msg := e.call(t, anna, "move_knowledge", map[string]any{"id": id, "space": "personal", "confirm": false}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("move confirm=false: %q", msg)
	}
	res = e.mustCall(t, anna, "move_knowledge", map[string]any{"id": id, "space": "personal", "confirm": true})
	if res["result"] != "moved" || res["document"].(map[string]any)["space_type"] != "personal" {
		t.Fatalf("%v", res)
	}

	// deleting: author may not, space admin does not see the personal entry
	out = e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Team", "content": "beta Inhalt", "space": "write"})
	teamID := out["id"].(string)
	if _, msg := e.call(t, anna, "delete_knowledge", map[string]any{"id": teamID, "confirm": true}); !strings.Contains(msg, "berechtigung") {
		t.Errorf("Author löscht: %q", msg)
	}
	if _, msg := e.call(t, sadmin, "delete_knowledge", map[string]any{"id": teamID, "confirm": false}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("delete confirm=false: %q", msg)
	}
	if _, msg := e.call(t, sadmin, "delete_knowledge", map[string]any{"id": id, "confirm": true}); msg != "nicht gefunden" {
		t.Errorf("fremder persönlicher Eintrag: %q", msg)
	}
	e.mustCall(t, sadmin, "delete_knowledge", map[string]any{"id": teamID, "confirm": true})
	if _, msg := e.call(t, anna, "get_knowledge", map[string]any{"id": teamID}); msg != "nicht gefunden" {
		t.Errorf("gelöschter Eintrag: %q", msg)
	}
}
