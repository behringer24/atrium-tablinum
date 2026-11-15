package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// The permission matrix for skills (see permission_matrix_test.go for the method). Skills have
// their own life cycle: a saved version is a draft, a curator other than the author approves it
// with a risk class, a curator releases it; class C needs an instance admin. In a personal space
// the owner's versions are released at once and nobody else sees them.

// A skill is addressed by its name and, for approving, the digest of the version that was read;
// the matrix passes both around as "name|digest".
func mxRef(name, digest string) string { return name + "|" + digest }

func splitRef(ref string) (name, digest string) {
	name, digest, ok := strings.Cut(ref, "|")
	if !ok {
		digest = strings.Repeat("0", 64) // a reference to nothing: the digest is made up as well
	}
	return name, digest
}

// draftSkill saves a new skill as the given user and returns its reference.
func (m *matrix) draftSkill(email, space string, extra map[string]string) string {
	m.t.Helper()
	m.n++
	name := fmt.Sprintf("mx-skill-%d", m.n)
	args := map[string]any{"name": name, "files": skillPackage(name, "Skill "+name, extra)}
	if space != "" {
		args["space"] = space
	}
	m.e.mustCall(m.t, m.as(email), "store_skill", args)
	return mxRef(name, m.digestOf(email, name, space))
}

func (m *matrix) digestOf(email, name, space string) string {
	m.t.Helper()
	args := map[string]any{"skill": name, "version": 1}
	if space != "" {
		args["space"] = space
	}
	out := m.e.mustCall(m.t, m.as(email), "get_skill", args)
	return out["version"].(map[string]any)["digest"].(string)
}

// release has the instance admin approve and publish the first version of a skill.
func (m *matrix) release(ref, space, risk string, publish bool) {
	m.t.Helper()
	name, digest := splitRef(ref)
	a := map[string]any{"skill": name, "version": 1, "space": space}
	m.e.mustCall(m.t, m.as("boss@x.de"), "review_skill", map[string]any{"skill": name, "version": 1, "space": space,
		"decision": "approve", "risk_class": risk, "confirm": true, "digest": digest})
	if publish {
		a["confirm"] = true
		m.e.mustCall(m.t, m.as("boss@x.de"), "publish_skill", a)
	}
}

func (m *matrix) skillAs(email, tool string, args map[string]any) {
	m.t.Helper()
	m.e.mustCall(m.t, m.as(email), tool, args)
}

var withGuide = map[string]string{"references/guide.md": "Read this first."}

func publishedForeign(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, _ mxRole) (string, bool) {
		ref := m.draftSkill("writer@x.de", space, withGuide)
		m.release(ref, space, "A", true)
		return ref, true
	}
}

func draftForeign(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, _ mxRole) (string, bool) { return m.draftSkill("writer@x.de", space, nil), true }
}

func approvedForeign(space, risk string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, _ mxRole) (string, bool) {
		ref := m.draftSkill("writer@x.de", space, nil)
		m.release(ref, space, risk, false)
		return ref, true
	}
}

// ownDraftSkill: a draft written by the caller; callers who cannot write have none.
func ownDraftSkill(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, r mxRole) (string, bool) {
		if r.rank < rkAuthor {
			return "", false
		}
		return m.draftSkill(r.email, space, nil), true
	}
}

// ownPublishedSkill: the caller's skill, released by the instance admin.
func ownPublishedSkill(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, r mxRole) (string, bool) {
		if r.rank < rkAuthor {
			return "", false
		}
		ref := m.draftSkill(r.email, space, nil)
		m.release(ref, space, "A", true)
		return ref, true
	}
}

func blockedForeign(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, r mxRole) (string, bool) {
		ref, _ := publishedForeign(space)(m, r)
		name, _ := splitRef(ref)
		m.skillAs("boss@x.de", "block_skill", map[string]any{"skill": name, "version": 1, "space": space, "reason": "test", "confirm": true})
		return ref, true
	}
}

func archivedForeign(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, r mxRole) (string, bool) {
		ref, _ := publishedForeign(space)(m, r)
		name, _ := splitRef(ref)
		m.skillAs("boss@x.de", "archive_skill", map[string]any{"skill": name, "space": space, "confirm": true})
		return ref, true
	}
}

// byName builds the arguments of a call that names a skill, in a space (empty: not named).
func byName(space string, extra map[string]any) func(string) map[string]any {
	return func(ref string) map[string]any {
		name, _ := splitRef(ref)
		a := map[string]any{"skill": name}
		if space != "" {
			a["space"] = space
		}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}
}

// withDigestArg is byName for approving: the digest of the version is part of the call.
func withDigestArg(space string, extra map[string]any) func(string) map[string]any {
	return func(ref string) map[string]any {
		_, digest := splitRef(ref)
		a := byName(space, extra)(ref)
		a["digest"] = digest
		return a
	}
}

// skillWorked: a call about a skill that found it and did what was asked has no "result" field;
// a space that is not writable or a name that matches several spaces answers with one.
func skillWorked(out map[string]any, _ string) bool { return out["result"] == nil }

func listsSkill(out map[string]any, ref string) bool {
	name, _ := splitRef(ref)
	for _, n := range listedNames(out) {
		if n == name {
			return true
		}
	}
	return false
}

func newSkillArgs(space string) func(string) map[string]any {
	return func(string) map[string]any {
		name := "mx-new-" + uuid.NewString()[:8]
		a := map[string]any{"name": name, "files": skillPackage(name, "new skill", nil)}
		if space != "" {
			a["space"] = space
		}
		return a
	}
}

func nothingPrepared(*matrix, mxRole) (string, bool) { return "", true }

func skillOps(space string) []mxOp {
	one := map[string]any{"version": 1}
	confirm := map[string]any{"version": 1, "confirm": true}
	return []mxOp{
		{name: "list published", tool: "list_skills", min: rkReader, prep: publishedForeign(space), seen: listsSkill, hidden: rkReader,
			args: func(ref string) map[string]any {
				n, _ := splitRef(ref)
				return map[string]any{"space": space, "name": n}
			}},
		{name: "list foreign draft", tool: "list_skills", min: rkCurator, prep: draftForeign(space), seen: listsSkill,
			args: func(ref string) map[string]any {
				n, _ := splitRef(ref)
				return map[string]any{"space": space, "name": n, "all_versions": true}
			}},
		{name: "get published", tool: "get_skill", min: rkReader, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader, args: byName(space, nil)},
		{name: "get foreign draft", tool: "get_skill", min: rkCurator, prep: draftForeign(space), seen: skillWorked, hidden: rkReader, args: byName(space, one)},
		{name: "get a file", tool: "get_skill_resource", min: rkReader, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader,
			args: byName(space, map[string]any{"path": "references/guide.md"})},
		{name: "store", tool: "store_skill", min: rkAuthor, prep: nothingPrepared, args: newSkillArgs(space), seen: skillWorked, hidden: rkReader,
			ghost: func() map[string]any { a := newSkillArgs("no-such-space")(""); return a }()},
		{name: "new version of a foreign released skill", tool: "store_skill", min: rkAuthor, prep: publishedForeign(space), seen: skillWorked,
			args: func(ref string) map[string]any {
				n, _ := splitRef(ref)
				return map[string]any{"name": n, "space": space, "files": skillPackage(n, "second version", nil)}
			}},
		{name: "new version over a foreign draft", tool: "store_skill", min: rkCurator, prep: draftForeign(space), seen: skillWorked,
			args: func(ref string) map[string]any {
				n, _ := splitRef(ref)
				return map[string]any{"name": n, "space": space, "files": skillPackage(n, "second version", nil)}
			}},
		{name: "tag foreign", tool: "tag_skill", min: rkCurator, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader,
			args: byName(space, map[string]any{"tags": []string{"x"}})},
		{name: "tag own", tool: "tag_skill", min: rkAuthor, prep: ownDraftSkill(space), seen: skillWorked,
			args: byName(space, map[string]any{"tags": []string{"x"}})},
		{name: "approve foreign draft", tool: "review_skill", min: rkCurator, prep: draftForeign(space), seen: skillWorked, hidden: rkReader,
			args: withDigestArg(space, map[string]any{"version": 1, "decision": "approve", "risk_class": "A", "confirm": true})},
		{name: "return foreign draft", tool: "review_skill", min: rkCurator, prep: draftForeign(space), seen: skillWorked, hidden: rkReader,
			args: byName(space, map[string]any{"version": 1, "decision": "reject", "note": "rework"})},
		// Nobody approves or returns their own version; only an instance admin may (internal/skills).
		{name: "approve own draft", tool: "review_skill", min: rkInstance, prep: ownDraftSkill(space), seen: skillWorked,
			args: withDigestArg(space, map[string]any{"version": 1, "decision": "approve", "risk_class": "A", "confirm": true})},
		{name: "return own draft", tool: "review_skill", min: rkInstance, prep: ownDraftSkill(space), seen: skillWorked,
			args: byName(space, map[string]any{"version": 1, "decision": "reject", "note": "rework"})},
		{name: "publish approved", tool: "publish_skill", min: rkCurator, prep: approvedForeign(space, "A"), seen: skillWorked, hidden: rkReader, args: byName(space, confirm)},
		{name: "publish class C", tool: "publish_skill", min: rkInstance, prep: approvedForeign(space, "C"), seen: skillWorked, args: byName(space, confirm)},
		{name: "block", tool: "block_skill", min: rkCurator, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader,
			args: byName(space, map[string]any{"version": 1, "reason": "wrong advice", "confirm": true})},
		{name: "unblock", tool: "unblock_skill", min: rkAdmin, prep: blockedForeign(space), seen: skillWorked, hidden: rkReader,
			args: byName(space, map[string]any{"version": 1, "reason": "fixed", "confirm": true})},
		{name: "archive foreign", tool: "archive_skill", min: rkCurator, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader, args: byName(space, map[string]any{"confirm": true})},
		{name: "archive own", tool: "archive_skill", min: rkCurator, prep: ownDraftSkill(space), seen: skillWorked, args: byName(space, map[string]any{"confirm": true})},
		{name: "unarchive", tool: "unarchive_skill", min: rkCurator, prep: archivedForeign(space), seen: skillWorked, hidden: rkCurator, args: byName(space, map[string]any{"confirm": true})},
		{name: "move foreign", tool: "move_skill", min: rkCurator, prep: publishedForeign(space), hidden: rkReader,
			seen: func(out map[string]any, _ string) bool { return out["result"] == "moved" },
			args: byName(space, map[string]any{"target_space": "dst", "confirm": true})},
		{name: "move own", tool: "move_skill", min: rkCurator, prep: ownPublishedSkill(space),
			seen: func(out map[string]any, _ string) bool { return out["result"] == "moved" },
			args: byName(space, map[string]any{"target_space": "dst", "confirm": true})},
		// In a team space only an instance admin deletes a skill: not the space admin, not the author.
		{name: "delete foreign", tool: "delete_skill", min: rkInstance, prep: publishedForeign(space), seen: skillWorked, hidden: rkReader, args: byName(space, map[string]any{"confirm": true})},
		{name: "delete own", tool: "delete_skill", min: rkInstance, prep: ownDraftSkill(space), seen: skillWorked, args: byName(space, map[string]any{"confirm": true})},
	}
}

func TestPermissionMatrixSkills(t *testing.T) {
	m := newMatrix(t)
	m.run("team space", skillOps("open"), mxRoles)
}

// A personal space belongs to its owner alone, skills included.
func TestPermissionMatrixSkillsPersonalSpace(t *testing.T) {
	m := newMatrix(t)
	mine := func(m *matrix, _ mxRole) (string, bool) { return m.draftSkill("owner@x.de", "", withGuide), true }
	ops := []mxOp{
		{name: "list", tool: "list_skills", min: rkOwner, prep: mine, seen: listsSkill, hidden: rkOwner,
			args: func(ref string) map[string]any { n, _ := splitRef(ref); return map[string]any{"name": n} }},
		{name: "get", tool: "get_skill", min: rkOwner, prep: mine, seen: skillWorked, hidden: rkOwner, args: byName("", nil)},
		{name: "get a file", tool: "get_skill_resource", min: rkOwner, prep: mine, seen: skillWorked, hidden: rkOwner,
			args: byName("", map[string]any{"path": "references/guide.md"})},
		{name: "tag", tool: "tag_skill", min: rkOwner, prep: mine, seen: skillWorked, hidden: rkOwner, args: byName("", map[string]any{"tags": []string{"x"}})},
		{name: "archive", tool: "archive_skill", min: rkOwner, prep: mine, seen: skillWorked, hidden: rkOwner, args: byName("", map[string]any{"confirm": true})},
		{name: "delete", tool: "delete_skill", min: rkOwner, prep: mine, seen: skillWorked, hidden: rkOwner, args: byName("", map[string]any{"confirm": true})},
	}
	m.run("personal space", ops, append([]mxRole{{"owner", "owner@x.de", rkOwner}}, mxRoles...))
}

// An archived team space is visible to instance admins only, and nothing is changed in it.
func TestPermissionMatrixSkillsArchivedSpace(t *testing.T) {
	m := newMatrix(t)
	old := m.e.team(t, "old", "Old", false, map[string]authz.Role{
		"reader@x.de": authz.Reader, "author@x.de": authz.Author, "curator@x.de": authz.Curator,
		"spaceadmin@x.de": authz.Admin, "writer@x.de": authz.Curator,
	})
	setArchived := func(on bool) {
		if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, on); err != nil {
			t.Fatal(err)
		}
	}
	fresh := func(m *matrix, r mxRole) (string, bool) {
		setArchived(false)
		ref, _ := publishedForeign("old")(m, r)
		setArchived(true)
		return ref, true
	}
	confirm := map[string]any{"confirm": true}
	ops := []mxOp{
		{name: "get", tool: "get_skill", min: rkInstance, prep: fresh, seen: skillWorked, hidden: rkInstance, args: byName("old", nil)},
		{name: "list", tool: "list_skills", min: rkInstance, prep: fresh, seen: listsSkill, hidden: rkInstance,
			args: func(ref string) map[string]any {
				n, _ := splitRef(ref)
				return map[string]any{"space": "old", "name": n}
			}},
		{name: "store", tool: "store_skill", min: never, prep: nothingPrepared, seen: skillWorked, hidden: rkInstance,
			args:  newSkillArgs("old"),
			ghost: newSkillArgs("no-such-space")("")},
		{name: "tag", tool: "tag_skill", min: never, prep: fresh, seen: skillWorked, hidden: rkInstance, args: byName("old", map[string]any{"tags": []string{"x"}})},
		{name: "archive", tool: "archive_skill", min: never, prep: fresh, seen: skillWorked, hidden: rkInstance, args: byName("old", confirm)},
		{name: "delete", tool: "delete_skill", min: never, prep: fresh, seen: skillWorked, hidden: rkInstance, args: byName("old", confirm)},
	}
	m.run("archived space", ops, mxRoles)
}
