package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// publishedSkill stores, approves and releases a skill of class A in the space as author
// and curator, so that tests can start from a released skill.
func publishedSkill(t *testing.T, e *env, author, curator *mcp.ClientSession, name, space string) {
	t.Helper()
	e.mustCall(t, author, "store_skill", map[string]any{"name": name, "space": space, "files": skillPackage(name, "Skill "+name, nil)})
	e.mustCall(t, curator, "review_skill", map[string]any{"skill": name, "version": 1, "decision": "approve", "risk_class": "A", "confirm": true})
	e.mustCall(t, curator, "publish_skill", map[string]any{"skill": name, "version": 1, "confirm": true})
}

func auditCount(t *testing.T, e *env, action string) int {
	t.Helper()
	var n int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSkillArchive(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader,
	})
	anna, cora, bob, boss := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "boss@x.de")
	publishedSkill(t, e, anna, cora, "code-review", "dev")

	// Readers and authors cannot archive; it needs a curator and a confirmation.
	for _, cs := range []*mcp.ClientSession{bob, anna} {
		if _, msg := e.call(t, cs, "archive_skill", map[string]any{"skill": "code-review", "confirm": true}); !strings.Contains(msg, "permission") {
			t.Fatalf("archived without the role: %q", msg)
		}
	}
	if _, msg := e.call(t, cora, "archive_skill", map[string]any{"skill": "code-review"}); !strings.Contains(msg, "confirm") {
		t.Fatalf("archive without confirmation: %q", msg)
	}
	e.mustCall(t, cora, "archive_skill", map[string]any{"skill": "code-review", "confirm": true})

	// Gone for everybody who cannot restore it: list, load by name and number, instructions.
	for _, cs := range []*mcp.ClientSession{bob, anna} {
		if names := listedNames(e.mustCall(t, cs, "list_skills", map[string]any{"all_versions": true})); len(names) != 0 {
			t.Fatalf("an archived skill is listed: %v", names)
		}
		if names := listedNames(e.mustCall(t, cs, "list_skills", map[string]any{"archived": true})); len(names) != 0 {
			t.Fatalf("the archive is visible to a non-curator: %v", names)
		}
		if _, msg := e.call(t, cs, "get_skill", map[string]any{"skill": "code-review"}); msg != "not found" {
			t.Fatalf("an archived skill is served: %q", msg)
		}
		if _, msg := e.call(t, cs, "get_skill", map[string]any{"skill": "code-review", "version": 1}); msg != "not found" {
			t.Fatalf("an archived skill is served by number: %q", msg)
		}
	}
	if in := e.session(t, "bob@x.de").InitializeResult().Instructions; strings.Contains(in, "code-review") {
		t.Fatalf("an archived skill is still announced: %q", in)
	}
	// Curators and instance admins find it in the archive, not in the normal list.
	if names := listedNames(e.mustCall(t, cora, "list_skills", nil)); len(names) != 0 {
		t.Fatalf("an archived skill is in the normal list: %v", names)
	}
	for _, cs := range []*mcp.ClientSession{cora, boss} {
		list := e.mustCall(t, cs, "list_skills", map[string]any{"archived": true})
		if names := listedNames(list); len(names) != 1 || names[0] != "code-review" {
			t.Fatalf("archive list: %v", list)
		}
	}
	// An archived skill takes no new versions and no review or release.
	if _, msg := e.call(t, anna, "store_skill", map[string]any{"name": "code-review", "space": "dev", "files": skillPackage("code-review", "again", nil)}); !strings.Contains(msg, "archived") {
		t.Fatalf("new version of an archived skill: %q", msg)
	}
	if _, msg := e.call(t, cora, "publish_skill", map[string]any{"skill": "code-review", "version": 1, "confirm": true}); !strings.Contains(msg, "archived") {
		t.Fatalf("release of an archived skill: %q", msg)
	}
	// Archiving twice is not possible: the skill is no longer in the active list.
	if _, msg := e.call(t, cora, "archive_skill", map[string]any{"skill": "code-review", "confirm": true}); msg != "not found" {
		t.Fatalf("archived skill archived again: %q", msg)
	}

	// Restoring is for curators only.
	if _, msg := e.call(t, bob, "unarchive_skill", map[string]any{"skill": "code-review", "confirm": true}); msg != "not found" {
		t.Fatalf("a reader restored a skill: %q", msg)
	}
	e.mustCall(t, cora, "unarchive_skill", map[string]any{"skill": "code-review", "confirm": true})
	if got := e.mustCall(t, bob, "get_skill", map[string]any{"skill": "code-review"}); got["content"] == "" {
		t.Fatalf("restored skill is not served: %v", got)
	}
	if _, msg := e.call(t, cora, "unarchive_skill", map[string]any{"skill": "code-review", "confirm": true}); msg != "not found" {
		t.Fatalf("active skill restored: %q", msg)
	}
	if auditCount(t, e, "skill_archive") != 1 || auditCount(t, e, "skill_unarchive") != 1 {
		t.Fatal("archive and restore are not in the audit log")
	}
}

func TestSkillUnblock(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader, "sam@x.de": authz.Admin,
	})
	anna, cora, bob, sam, boss := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "sam@x.de"), e.session(t, "boss@x.de")
	publishedSkill(t, e, anna, cora, "code-review", "dev")
	e.mustCall(t, cora, "block_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "wrong advice", "confirm": true})

	// A curator blocks but cannot lift the block: the tool is not even offered.
	if toolNames(t, e, cora)["unblock_skill"] {
		t.Fatal("a curator is offered unblock_skill")
	}
	if res, err := cora.CallTool(e.ctx, &mcp.CallToolParams{Name: "unblock_skill", Arguments: map[string]any{
		"skill": "code-review", "version": 1, "reason": "x", "confirm": true}}); err == nil && !res.IsError {
		t.Fatal("a curator lifted a block")
	}
	if !toolNames(t, e, sam)["unblock_skill"] {
		t.Fatal("a space admin is not offered unblock_skill")
	}
	// Confirmation and a reason are required.
	if _, msg := e.call(t, sam, "unblock_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "fixed"}); !strings.Contains(msg, "confirm") {
		t.Fatalf("unblock without confirmation: %q", msg)
	}
	if _, msg := e.call(t, sam, "unblock_skill", map[string]any{"skill": "code-review", "version": 1, "confirm": true}); !strings.Contains(msg, "reason") {
		t.Fatalf("unblock without reason: %q", msg)
	}
	m := e.mustCall(t, sam, "unblock_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "advice corrected upstream", "confirm": true})
	// The version is approved again, not released: readers still get nothing.
	if versionStatus(t, m) != "approved" {
		t.Fatalf("unblock: %v", m)
	}
	if _, msg := e.call(t, bob, "get_skill", map[string]any{"skill": "code-review"}); msg != "not found" {
		t.Fatalf("an unblocked version is served without release: %q", msg)
	}
	if _, msg := e.call(t, sam, "unblock_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "again", "confirm": true}); !strings.Contains(msg, "state") {
		t.Fatalf("unblock of a version that is not blocked: %q", msg)
	}
	e.mustCall(t, cora, "publish_skill", map[string]any{"skill": "code-review", "version": 1, "confirm": true})
	if got := e.mustCall(t, bob, "get_skill", map[string]any{"skill": "code-review"}); !strings.Contains(fmt.Sprint(got["content"]), "Run the tests") {
		t.Fatalf("released again: %v", got)
	}

	// Instance admins may as well.
	e.mustCall(t, cora, "block_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "second", "confirm": true})
	e.mustCall(t, boss, "unblock_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "second look", "confirm": true})
	if auditCount(t, e, "skill_unblock") != 2 {
		t.Fatal("unblock is not in the audit log")
	}

	// The database refuses every other way out of blocked: only blocked -> approved exists.
	e.mustCall(t, cora, "block_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "third", "confirm": true})
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE skill_versions SET status = 'published'`); err == nil {
		t.Fatal("a blocked version went straight back to published")
	}
}

func TestSkillDelete(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader, "sam@x.de": authz.Admin,
	})
	anna, cora, bob, sam, boss := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "sam@x.de"), e.session(t, "boss@x.de")
	publishedSkill(t, e, anna, cora, "code-review", "dev")
	publishedSkill(t, e, anna, cora, "release-notes", "dev")

	// Only instance admins delete in a team space: not the author, curator or space admin.
	for _, cs := range []*mcp.ClientSession{bob, anna, cora, sam} {
		if _, msg := e.call(t, cs, "delete_skill", map[string]any{"skill": "code-review", "confirm": true}); !strings.Contains(msg, "permission") {
			t.Fatalf("deleted without being an instance admin: %q", msg)
		}
	}
	if _, msg := e.call(t, boss, "delete_skill", map[string]any{"skill": "code-review"}); !strings.Contains(msg, "confirm") {
		t.Fatalf("delete without confirmation: %q", msg)
	}
	e.mustCall(t, boss, "delete_skill", map[string]any{"skill": "code-review", "confirm": true})
	if _, msg := e.call(t, bob, "get_skill", map[string]any{"skill": "code-review"}); msg != "not found" {
		t.Fatalf("a deleted skill is served: %q", msg)
	}
	// Name, versions and digest stay in the audit log; the other skill is untouched.
	var details string
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT details::text FROM audit_log WHERE action = 'skill_delete'`).Scan(&details); err != nil ||
		!strings.Contains(details, "code-review") || !strings.Contains(details, "digest") {
		t.Fatalf("audit of the delete: %q %v", details, err)
	}
	var skillsLeft, versionsLeft, filesLeft int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_versions), (SELECT count(*) FROM skill_resources)`).
		Scan(&skillsLeft, &versionsLeft, &filesLeft); err != nil || skillsLeft != 1 || versionsLeft != 1 || filesLeft != 1 {
		t.Fatalf("after delete: %d skills, %d versions, %d files (%v)", skillsLeft, versionsLeft, filesLeft, err)
	}
	if got := e.mustCall(t, bob, "get_skill", map[string]any{"skill": "release-notes"}); got["content"] == "" {
		t.Fatalf("another skill was hit: %v", got)
	}

	// An archived skill can be deleted too, and the name is free again afterwards.
	e.mustCall(t, cora, "archive_skill", map[string]any{"skill": "release-notes", "confirm": true})
	e.mustCall(t, boss, "delete_skill", map[string]any{"skill": "release-notes", "confirm": true})
	if names := listedNames(e.mustCall(t, boss, "list_skills", map[string]any{"archived": true})); len(names) != 0 {
		t.Fatalf("deleted skill still in the archive: %v", names)
	}
	e.mustCall(t, anna, "store_skill", map[string]any{"name": "release-notes", "space": "dev", "files": skillPackage("release-notes", "Again", nil)})

	// In a personal space the owner deletes; nobody else can reach it.
	e.mustCall(t, anna, "store_skill", map[string]any{"name": "my-notes", "space": "my personal space", "files": skillPackage("my-notes", "Private", nil)})
	if _, msg := e.call(t, boss, "delete_skill", map[string]any{"skill": "my-notes", "confirm": true}); msg != "not found" {
		t.Fatalf("an instance admin reached a personal skill: %q", msg)
	}
	e.mustCall(t, anna, "delete_skill", map[string]any{"skill": "my-notes", "confirm": true})
	if _, msg := e.call(t, anna, "get_skill", map[string]any{"skill": "my-notes"}); msg != "not found" {
		t.Fatalf("a deleted personal skill is served: %q", msg)
	}
}
