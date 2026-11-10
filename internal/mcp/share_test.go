package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/share"
)

func entryIDs(m map[string]any) []string {
	var out []string
	list, _ := m["entries"].([]any)
	for _, x := range list {
		out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
	}
	return out
}

func storePersonal(t *testing.T, e *env, cs *mcp.ClientSession, title string) string {
	t.Helper()
	m := e.mustCall(t, cs, "store_knowledge", map[string]any{"title": title, "content": "Content of " + title + " about something.", "force": true})
	return fmt.Sprint(m["id"])
}

func TestShareToolsExistOnlyWhenEnabled(t *testing.T) {
	off := setup(t, 0)
	if tools := toolNames(t, off, off.session(t, "anna@x.de")); tools["share_knowledge"] || tools["share_skill"] {
		t.Error("share tools are listed although sharing is off")
	}
	on := setupSharing(t, 0, share.Options{Enabled: true})
	if tools := toolNames(t, on, on.session(t, "anna@x.de")); !tools["share_knowledge"] || !tools["share_skill"] {
		t.Error("share tools are missing although sharing is on")
	}
}

func TestShareKnowledgeEndToEnd(t *testing.T) {
	e := setupSharing(t, 0, share.Options{Enabled: true})
	anna, ben := e.session(t, "anna@x.de"), e.session(t, "ben@x.de")
	id := storePersonal(t, e, anna, "Deploy checklist")

	// Needs a confirmation.
	if _, msg := e.call(t, anna, "share_knowledge", map[string]any{"id": id, "recipient": "ben@x.de"}); !strings.Contains(msg, "confirmation") {
		t.Fatalf("send without confirmation: %q", msg)
	}
	e.mustCall(t, anna, "share_knowledge", map[string]any{"id": id, "recipient": "ben@x.de", "confirm": true})

	// It waits as a draft in Ben's personal space, with Anna as author, and nothing else changed for Anna.
	drafts := e.mustCall(t, ben, "list_knowledge", map[string]any{"status": "draft"})
	ids := entryIDs(drafts)
	if len(ids) != 1 {
		t.Fatalf("Ben's drafts: %v", drafts)
	}
	got := e.mustCall(t, ben, "get_knowledge", map[string]any{"id": ids[0]})
	if got["author"] != "anna@x.de" || got["status"] != "draft" || got["title"] != "Deploy checklist" {
		t.Fatalf("received copy: %v", got)
	}
	if news := e.mustCall(t, ben, "whats_new", nil); news["sent_to_you"] == nil {
		t.Errorf("whats_new does not mention what was sent: %v", news)
	}
	// The sender cannot read, change or review the copy, and Ben's own search does not serve a draft as knowledge of the team.
	if _, msg := e.call(t, anna, "get_knowledge", map[string]any{"id": ids[0]}); msg != "not found" {
		t.Errorf("the sender reads the copy: %q", msg)
	}
	if _, msg := e.call(t, anna, "update_knowledge", map[string]any{"id": ids[0], "title": "x"}); msg != "not found" {
		t.Errorf("the sender changes the copy: %q", msg)
	}
	if _, msg := e.call(t, anna, "review_knowledge", map[string]any{"id": ids[0], "decision": "approve", "confirm": true}); msg != "not found" {
		t.Errorf("the sender approves the copy: %q", msg)
	}
	// The audit log records the sending without title or content.
	var details string
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT COALESCE(details::text, '') FROM audit_log WHERE action = 'share_send'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "Deploy") || !strings.Contains(details, "knowledge") {
		t.Errorf("share_send details: %s", details)
	}

	// Ben approves: the entry becomes a normal entry of his personal space.
	if _, msg := e.call(t, ben, "review_knowledge", map[string]any{"id": ids[0], "decision": "approve"}); !strings.Contains(msg, "confirmation") {
		t.Fatalf("approve without confirmation: %q", msg)
	}
	ok := e.mustCall(t, ben, "review_knowledge", map[string]any{"id": ids[0], "decision": "approve", "confirm": true})
	if ok["document"].(map[string]any)["status"] != "approved" {
		t.Fatalf("approved copy: %v", ok)
	}
	if ids := entryIDs(e.mustCall(t, ben, "list_knowledge", map[string]any{"status": "draft"})); len(ids) != 0 {
		t.Errorf("drafts after approval: %v", ids)
	}
}

func TestShareKnowledgeRefusals(t *testing.T) {
	e := setupSharing(t, 0, share.Options{Enabled: true, MaxPerRecipient: 3, MaxPerSender: 2})
	e.team(t, "wiki", "Wiki", false, map[string]authz.Role{"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "rob@x.de": authz.Reader})
	anna, ben, cora, rob := e.session(t, "anna@x.de"), e.session(t, "ben@x.de"), e.session(t, "cora@x.de"), e.session(t, "rob@x.de")
	e.session(t, "rita@x.de") // an account that is disabled below
	send := func(cs *mcp.ClientSession, id, to string) string {
		_, msg := e.call(t, cs, "share_knowledge", map[string]any{"id": id, "recipient": to, "confirm": true})
		return msg
	}
	a1, a2, a3 := storePersonal(t, e, anna, "One"), storePersonal(t, e, anna, "Two"), storePersonal(t, e, anna, "Three")

	// Unknown recipient, himself, and a recipient whose space is full all give the one answer a sender
	// may learn (the first two their own).
	unknown := send(anna, a1, "nobody@x.de")
	if !strings.Contains(unknown, "could not be delivered") {
		t.Fatalf("unknown recipient: %q", unknown)
	}
	if msg := send(anna, a1, "anna@x.de"); !strings.Contains(msg, "yourself") {
		t.Errorf("to himself: %q", msg)
	}
	if msg := send(anna, a1, ""); !strings.Contains(msg, "address") {
		t.Errorf("no recipient: %q", msg)
	}
	// An account that is disabled cannot receive, again without saying so.
	boss := e.session(t, "boss@x.de")
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "rita@x.de", "confirm": true})
	if msg := send(anna, a1, "rita@x.de"); msg != unknown {
		t.Errorf("disabled recipient answers differently: %q vs %q", msg, unknown)
	}

	// Per sender: two items wait at most; the third is refused with the same words.
	if msg := send(anna, a1, "ben@x.de"); msg != "" {
		t.Fatal(msg)
	}
	if msg := send(anna, a2, "ben@x.de"); msg != "" {
		t.Fatal(msg)
	}
	if msg := send(anna, a3, "ben@x.de"); msg != unknown {
		t.Errorf("sender limit: %q", msg)
	}
	// Per recipient: somebody else fills the remaining place, then nobody can send.
	c1, c2 := storePersonal(t, e, cora, "C one"), storePersonal(t, e, cora, "C two")
	if msg := send(cora, c1, "ben@x.de"); msg != "" {
		t.Fatal(msg)
	}
	if msg := send(cora, c2, "ben@x.de"); msg != unknown {
		t.Errorf("recipient limit: %q", msg)
	}
	// A returned item still counts until Ben archives it.
	drafts := entryIDs(e.mustCall(t, ben, "list_knowledge", map[string]any{"status": "draft"}))
	if len(drafts) != 3 {
		t.Fatalf("Ben's drafts: %v", drafts)
	}
	e.mustCall(t, ben, "review_knowledge", map[string]any{"id": drafts[0], "decision": "reject", "note": "not for me"})
	if msg := send(cora, c2, "ben@x.de"); msg != unknown {
		t.Errorf("a returned item stopped counting: %q", msg)
	}
	e.mustCall(t, ben, "archive_knowledge", map[string]any{"id": drafts[0], "confirm": true})
	if msg := send(cora, c2, "ben@x.de"); msg != "" {
		t.Errorf("after archiving there is room again: %q", msg)
	}

	// What may be sent: only approved entries the sender may change. A reader's own view of a team entry is not enough.
	team := e.mustCall(t, cora, "store_knowledge", map[string]any{"title": "Team rule", "content": "Binding text for the whole team.", "space": "wiki", "type": "rule", "force": true})
	if msg := send(rob, fmt.Sprint(team["id"]), "anna@x.de"); msg == "" {
		t.Error("a reader sent a team entry")
	}
	if msg := send(cora, fmt.Sprint(team["id"]), "anna@x.de"); msg != "" {
		t.Fatal(msg)
	}
	annaDrafts := e.mustCall(t, anna, "list_knowledge", map[string]any{"status": "draft"})
	got := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": entryIDs(annaDrafts)[0]})
	if got["type"] != "documentation" {
		t.Errorf("a rule must arrive as documentation: %v", got["type"])
	}
}

func TestShareSkillEndToEnd(t *testing.T) {
	e := setupSharing(t, 0, share.Options{Enabled: true})
	anna, ben := e.session(t, "anna@x.de"), e.session(t, "ben@x.de")
	e.mustCall(t, anna, "store_skill", map[string]any{"name": "code-review", "files": skillPackage("code-review", "Review a change", nil)})

	if _, msg := e.call(t, anna, "share_skill", map[string]any{"skill": "code-review", "recipient": "ben@x.de"}); !strings.Contains(msg, "confirmation") {
		t.Fatalf("send without confirmation: %q", msg)
	}
	e.mustCall(t, anna, "share_skill", map[string]any{"skill": "code-review", "recipient": "ben@x.de", "confirm": true})

	// Ben's assistant does not offer it yet; it is a draft with Anna named as its author.
	if names := listedNames(e.mustCall(t, ben, "list_skills", nil)); len(names) != 0 {
		t.Fatalf("a skill nobody reviewed is offered: %v", names)
	}
	listed := e.mustCall(t, ben, "list_skills", map[string]any{"status": "draft"})
	if names := listedNames(listed); len(names) != 1 || names[0] != "code-review" {
		t.Fatalf("Ben's drafts: %v", listed)
	}
	v := listed["skills"].([]any)[0].(map[string]any)["versions"].([]any)[0].(map[string]any)
	if v["author"] != "anna@x.de" || v["status"] != "draft" {
		t.Errorf("received version: %v", v)
	}
	if news := e.mustCall(t, ben, "whats_new", nil); news["sent_to_you"] == nil {
		t.Errorf("whats_new does not mention what was sent: %v", news)
	}
	// The same name in his space makes the second send fail like any other undeliverable one.
	if _, msg := e.call(t, anna, "share_skill", map[string]any{"skill": "code-review", "recipient": "ben@x.de", "confirm": true}); !strings.Contains(msg, "could not be delivered") {
		t.Errorf("name already taken there: %q", msg)
	}
	// Anna can neither review nor release it.
	if _, msg := e.call(t, anna, "list_skills", map[string]any{"status": "draft"}); msg != "" {
		t.Fatal(msg)
	}

	e.mustCall(t, ben, "review_skill", map[string]any{"skill": "code-review", "version": 1, "decision": "approve", "risk_class": "A", "confirm": true})
	e.mustCall(t, ben, "publish_skill", map[string]any{"skill": "code-review", "version": 1, "confirm": true})
	got := e.mustCall(t, ben, "get_skill", map[string]any{"skill": "code-review"})
	if got["content"] == nil || versionStatus(t, got) != "published" {
		t.Fatalf("released copy: %v", got)
	}
	// Only a released skill with the right of the sender can be sent.
	e.team(t, "dev", "Development", false, map[string]authz.Role{"cora@x.de": authz.Author, "dora@x.de": authz.Curator})
	cora, dora := e.session(t, "cora@x.de"), e.session(t, "dora@x.de")
	e.mustCall(t, cora, "store_skill", map[string]any{"name": "team-skill", "space": "dev", "files": skillPackage("team-skill", "For the team", nil)})
	if _, msg := e.call(t, dora, "share_skill", map[string]any{"skill": "team-skill", "recipient": "ben@x.de", "confirm": true}); !strings.Contains(msg, "release") {
		t.Errorf("an unreleased skill was sent: %q", msg)
	}
	e.mustCall(t, dora, "review_skill", map[string]any{"skill": "team-skill", "version": 1, "decision": "approve", "risk_class": "A", "confirm": true})
	e.mustCall(t, dora, "publish_skill", map[string]any{"skill": "team-skill", "version": 1, "confirm": true})
	if _, msg := e.call(t, cora, "share_skill", map[string]any{"skill": "team-skill", "recipient": "ben@x.de", "confirm": true}); msg == "" {
		t.Error("an author sent a skill of a team space")
	}
	e.mustCall(t, dora, "share_skill", map[string]any{"skill": "team-skill", "recipient": "ben@x.de", "confirm": true})
	if names := listedNames(e.mustCall(t, ben, "list_skills", map[string]any{"status": "draft"})); len(names) != 1 || names[0] != "team-skill" {
		t.Errorf("Ben's drafts: %v", names)
	}
}
