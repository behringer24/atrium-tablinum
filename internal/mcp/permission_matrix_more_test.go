package mcpserver

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/share"
)

// More cells of the permission matrix (method: permission_matrix_test.go): sending an entry or a
// skill, the ZIP route of skills, tools that an edition adds, the excerpts of search hits, and
// the versions and counts that whats_new reports. The expectations are written out by hand.

// manySends lifts the limits of senders and recipients: the matrix sends a lot to one account.
var manySends = share.Options{Enabled: true, MaxPerRecipient: 100000, MaxPerSender: 100000}

const recipient = "recipient@x.de"

// ---- sending -----------------------------------------------------------------------------

func shareOps(space string) []mxOp {
	to := map[string]any{"recipient": recipient, "confirm": true}
	return []mxOp{
		// Sending needs the right to change the entry: its author, or a curator of the space.
		{name: "send foreign entry", tool: "share_knowledge", min: rkCurator, prep: foreign(space), args: idArgs(to), hidden: rkReader},
		{name: "send own entry", tool: "share_knowledge", min: rkAuthor, args: idArgs(to),
			prep: func(m *matrix, r mxRole) (string, bool) {
				if space == "gated" && r.rank < rkAdmin {
					return "", false // below admin an own entry is a draft there, and drafts are not sent
				}
				return own(space, nil)(m, r)
			}},
		// Only approved entries can be sent, never somebody's draft.
		{name: "send a draft", tool: "share_knowledge", min: never, hidden: rkCurator, args: idArgs(to),
			prep: func(m *matrix, _ mxRole) (string, bool) {
				return m.store("drafter@x.de", map[string]any{"space": space, "draft": true}), true
			}},
		{name: "send a released skill", tool: "share_skill", min: rkCurator, prep: publishedForeign(space), hidden: rkReader, args: byName(space, to)},
		{name: "send an own released skill", tool: "share_skill", min: rkCurator, prep: ownPublishedSkill(space), args: byName(space, to)},
		// Only a released version can be sent.
		{name: "send a skill without release", tool: "share_skill", min: never, prep: draftForeign(space), hidden: rkCurator, args: byName(space, to)},
		{name: "send an archived skill", tool: "share_skill", min: never, prep: archivedForeign(space), hidden: rkCurator, args: byName(space, to)},
	}
}

func TestPermissionMatrixSending(t *testing.T) {
	m := newMatrixSharing(t, manySends)
	m.run("open space", shareOps("open"), mxRoles)
	m.run("approval space", shareOps("gated"), mxRoles)
}

func TestPermissionMatrixSendingPersonalSpace(t *testing.T) {
	m := newMatrixSharing(t, manySends)
	to := map[string]any{"recipient": recipient, "confirm": true}
	entry := func(m *matrix, _ mxRole) (string, bool) { return m.store("owner@x.de", nil), true }
	skill := func(m *matrix, _ mxRole) (string, bool) { return m.draftSkill("owner@x.de", "", withGuide), true }
	ops := []mxOp{
		{name: "send entry", tool: "share_knowledge", min: rkOwner, prep: entry, args: idArgs(to), hidden: rkOwner},
		{name: "send skill", tool: "share_skill", min: rkOwner, prep: skill, args: byName("", to), hidden: rkOwner},
	}
	m.run("personal space", ops, append([]mxRole{{"owner", "owner@x.de", rkOwner}}, mxRoles...))
}

// Nothing is sent out of an archived space.
func TestPermissionMatrixSendingArchivedSpace(t *testing.T) {
	m := newMatrixSharing(t, manySends)
	old := m.e.team(t, "old", "Old", false, map[string]authz.Role{
		"reader@x.de": authz.Reader, "author@x.de": authz.Author, "curator@x.de": authz.Curator,
		"spaceadmin@x.de": authz.Admin, "writer@x.de": authz.Curator,
	})
	setArchived := func(on bool) {
		if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, on); err != nil {
			t.Fatal(err)
		}
	}
	to := map[string]any{"recipient": recipient, "confirm": true}
	entry := func(m *matrix, _ mxRole) (string, bool) {
		setArchived(false)
		id := m.store("writer@x.de", map[string]any{"space": "old"})
		setArchived(true)
		return id, true
	}
	skill := func(m *matrix, r mxRole) (string, bool) {
		setArchived(false)
		ref, _ := publishedForeign("old")(m, r)
		setArchived(true)
		return ref, true
	}
	ops := []mxOp{
		{name: "send entry", tool: "share_knowledge", min: never, prep: entry, args: idArgs(to), hidden: rkInstance},
		{name: "send skill", tool: "share_skill", min: never, prep: skill, args: byName("old", to), hidden: rkInstance},
	}
	m.run("archived space", ops, mxRoles)
}

// ---- the ZIP route of skills -------------------------------------------------------------

// The route answers 404 for everything the caller may not fetch, and the 404 is the same for a
// skill that is not there at all.
func TestPermissionMatrixSkillExport(t *testing.T) {
	m := newMatrix(t)
	old := m.e.team(t, "old", "Old", false, map[string]authz.Role{"writer@x.de": authz.Curator})

	get := func(email, space, name string) (int, string) {
		res, body := m.e.exportGet(t, email, fmt.Sprintf("/skills/export/%s/%s", space, name))
		return res.StatusCode, string(body)
	}
	name := func(ref string) string { n, _ := splitRef(ref); return n }

	released := publishedForeign("open")
	cases := []struct {
		label string
		make  func() (space, skill string)
		min   int // lowest rank that gets the ZIP
	}{
		{"released skill", func() (string, string) { ref, _ := released(m, mxRole{}); return "open", name(ref) }, rkReader},
		{"released skill, space with approval", func() (string, string) {
			ref, _ := publishedForeign("gated")(m, mxRole{})
			return "gated", name(ref)
		}, rkReader},
		{"skill without release", func() (string, string) { ref, _ := draftForeign("open")(m, mxRole{}); return "open", name(ref) }, never},
		{"approved, not released", func() (string, string) {
			ref, _ := approvedForeign("open", "A")(m, mxRole{})
			return "open", name(ref)
		}, never},
		{"blocked skill", func() (string, string) { ref, _ := blockedForeign("open")(m, mxRole{}); return "open", name(ref) }, never},
		{"archived skill", func() (string, string) { ref, _ := archivedForeign("open")(m, mxRole{}); return "open", name(ref) }, never},
		{"skill in an archived space", func() (string, string) {
			if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, false); err != nil {
				t.Fatal(err)
			}
			ref, _ := publishedForeign("old")(m, mxRole{})
			if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, true); err != nil {
				t.Fatal(err)
			}
			return "old", name(ref)
		}, rkInstance},
	}
	for _, c := range cases {
		space, skill := c.make()
		for _, r := range mxRoles {
			t.Run(c.label+"/"+r.name, func(t *testing.T) {
				code, body := get(r.email, space, skill)
				want := http.StatusNotFound
				if r.rank >= c.min {
					want = http.StatusOK
				}
				if code != want {
					t.Fatalf("status %d, the rule says %d (%s)", code, want, strings.TrimSpace(body))
				}
				if code == http.StatusNotFound {
					if gcode, gbody := get(r.email, space, "no-such-skill-"+skill); gcode != code || gbody != body {
						t.Fatalf("a refusal differs from the answer for a skill that does not exist: %d %q, %d %q", code, body, gcode, gbody)
					}
				}
			})
		}
	}

	// A personal space belongs to its owner alone.
	mine := m.draftSkill("owner@x.de", "", withGuide)
	personal := ""
	for _, sp := range m.e.mustCall(t, m.as("owner@x.de"), "list_spaces", nil)["spaces"].([]any) {
		if sp.(map[string]any)["type"] == "personal" {
			personal = sp.(map[string]any)["slug"].(string)
		}
	}
	if personal == "" {
		t.Fatal("the owner has no personal space")
	}
	for _, r := range append([]mxRole{{"owner", "owner@x.de", rkOwner}}, mxRoles...) {
		t.Run("personal skill/"+r.name, func(t *testing.T) {
			code, _ := get(r.email, personal, name(mine))
			want := http.StatusNotFound
			if r.rank == rkOwner {
				want = http.StatusOK
			}
			if code != want {
				t.Fatalf("status %d, the rule says %d", code, want)
			}
		})
	}

	// No login, no ZIP.
	if code, _ := get("", "open", "anything"); code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", code)
	}
}

// ---- tools of an edition -----------------------------------------------------------------

// A tool that an edition adds is offered, and callable, for the holders of its group only; for
// everyone else it does not exist, which is the same answer as for a tool that nobody has.
func TestPermissionMatrixEditionTools(t *testing.T) {
	saved := editionTools
	t.Cleanup(func() { editionTools = saved })
	editionTools = append(editionTools[:len(editionTools):len(editionTools)], func(s *Server, ms *mcp.Server, group tier) {
		switch group {
		case tierUser:
			addTool(s, ms, "ed_user", "Edition user tool", tierUser, false, noop)
		case tierCurator:
			addTool(s, ms, "ed_curator", "Edition curator tool", tierCurator, false, noop)
		case tierSpaceAdmin:
			addTool(s, ms, "ed_spaceadmin", "Edition space admin tool", tierSpaceAdmin, false, noop)
		case tierInstanceAdmin:
			addTool(s, ms, "ed_instanceadmin", "Edition instance admin tool", tierInstanceAdmin, false, noop)
		}
	})
	m := newMatrix(t)
	op := func(tool string, min int) mxOp {
		return mxOp{name: tool, tool: tool, min: min, prep: nothingPrepared, args: func(string) map[string]any { return nil }}
	}
	m.run("edition", []mxOp{op("ed_user", rkOutsider), op("ed_curator", rkCurator), op("ed_spaceadmin", rkAdmin), op("ed_instanceadmin", rkInstance)}, mxRoles)

	// The refusal does not tell the caller that such a tool exists.
	_, refused := m.callTool(t, "outsider@x.de", "ed_instanceadmin", nil)
	_, missing := m.callTool(t, "outsider@x.de", "ed_no_such_tool", nil)
	if strings.ReplaceAll(refused, "ed_instanceadmin", "X") != strings.ReplaceAll(missing, "ed_no_such_tool", "X") {
		t.Fatalf("an edition tool shows itself to a caller without the role: %q, for a missing one: %q", refused, missing)
	}
}

// ---- excerpts of search hits -------------------------------------------------------------

// What a search shows of an entry (its excerpt, title, status) follows the visibility of the
// entry: somebody else's draft, returned and archived entries only for those who may restore or
// review them, and a space the caller cannot see is as unknown as one that does not exist.
func searchOps(m *matrix, space string) []mxOp {
	query := func(extra map[string]any) func(string) map[string]any {
		return func(string) map[string]any {
			a := map[string]any{"query": fmt.Sprintf("mxtoken%d", m.n), "limit": 20}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
	}
	// a hit that is shown carries the entry's own text as its excerpt (other entries may match
	// the query as well; they are not looked at here)
	excerpt := func(_ mxRole, out map[string]any) string {
		hits, _ := out["results"].([]any)
		for _, h := range hits {
			if ex, _ := h.(map[string]any)["excerpt"].(string); strings.Contains(ex, fmt.Sprintf("mxtoken%d body text", m.n)) {
				return ""
			}
		}
		return "no hit carries the text of the entry"
	}
	found := func(out map[string]any, id string) bool { return containsID(out["results"], id) }
	draft := func(m *matrix, _ mxRole) (string, bool) {
		return m.store("drafter@x.de", map[string]any{"space": space, "draft": true}), true
	}
	ownDraft := func(m *matrix, r mxRole) (string, bool) {
		if r.rank < rkAuthor {
			return "", false
		}
		return m.store(r.email, map[string]any{"space": space, "draft": true}), true
	}
	returned := func(m *matrix, _ mxRole) (string, bool) {
		id := m.store("drafter@x.de", map[string]any{"space": space, "draft": true})
		m.e.mustCall(m.t, m.as("curator@x.de"), "review_knowledge", map[string]any{"id": id, "decision": "reject", "note": "please rework"})
		return id, true
	}
	archived := func(m *matrix, _ mxRole) (string, bool) {
		id := m.store("writer@x.de", map[string]any{"space": space})
		m.archive("writer@x.de", id)
		return id, true
	}
	ownArchived := func(m *matrix, r mxRole) (string, bool) {
		if r.rank < rkAuthor {
			return "", false
		}
		id := m.store(r.email, map[string]any{"space": space})
		m.archive(r.email, id)
		return id, true
	}
	ops := []mxOp{
		{name: "excerpt of an approved entry", tool: "search_knowledge", min: rkReader, prep: foreign(space), args: query(nil), seen: found, check: excerpt},
		{name: "excerpt of an approved entry, space named", tool: "search_knowledge", min: rkReader, prep: foreign(space),
			args: query(map[string]any{"spaces": []string{space}}), seen: found, check: excerpt, hidden: rkReader,
			ghost: map[string]any{"query": "mxtoken", "spaces": []string{"no-such-space"}}},
		{name: "foreign draft", tool: "search_knowledge", min: rkCurator, prep: draft, args: query(nil), seen: found, check: excerpt},
		{name: "own draft", tool: "search_knowledge", min: rkAuthor, prep: ownDraft, args: query(nil), seen: found, check: excerpt},
		{name: "returned entry", tool: "search_knowledge", min: rkCurator, prep: returned, args: query(nil), seen: found, check: excerpt},
		// an archived entry is not a normal hit at all, and the archive is for those who may restore it
		{name: "archived entry in the normal search", tool: "search_knowledge", min: never, prep: archived, args: query(nil), seen: found},
		{name: "archived entry in the archive search", tool: "search_knowledge", min: rkCurator, prep: archived,
			args: query(map[string]any{"archive": true}), seen: found, check: excerpt},
		{name: "own archived entry in the archive search", tool: "search_knowledge", min: rkAuthor, prep: ownArchived,
			args: query(map[string]any{"archive": true}), seen: found, check: excerpt},
	}
	return ops
}

func TestPermissionMatrixSearchExcerpts(t *testing.T) {
	m := newMatrix(t)
	m.run("open space", searchOps(m, "open"), mxRoles)
	m.run("approval space", searchOps(m, "gated"), mxRoles)
}

// ---- versions and counts in whats_new ----------------------------------------------------

// whats_new names the version of an entry and counts what waits for review. Neither may tell more
// than the entries themselves: no drafts of others, no archived entries, nothing from personal
// spaces, and the counts of waiting entries only for those who review them.
func newsOps(m *matrix, space string) []mxOp {
	news := func(extra map[string]any) func(string) map[string]any {
		return func(string) map[string]any {
			a := map[string]any{"spaces": []string{space}}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
	}
	ghost := map[string]any{"spaces": []string{"no-such-space"}}
	listed := func(out map[string]any, id string) bool { return containsID(out["entries"], id) }
	waiting := func(out map[string]any, _ string) bool {
		list, _ := out["waiting_for_review"].([]any)
		for _, w := range list {
			if e, _ := w.(map[string]any)["entries"].(float64); e >= 1 {
				return true
			}
		}
		return false
	}
	// the version shown for an entry is the version it has
	var target string // the entry of the current case
	version := func(want int) func(mxRole, map[string]any) string {
		return func(_ mxRole, out map[string]any) string {
			for _, e := range out["entries"].([]any) {
				if e.(map[string]any)["id"] != target {
					continue
				}
				if v, _ := e.(map[string]any)["version"].(float64); int(v) != want {
					return fmt.Sprintf("version %v, want %d", v, want)
				}
			}
			return ""
		}
	}
	updated := func(m *matrix, _ mxRole) (string, bool) {
		id := m.store("writer@x.de", map[string]any{"space": space})
		m.e.mustCall(m.t, m.as("writer@x.de"), "update_knowledge", map[string]any{"id": id, "title": "second version", "expected_version": 1})
		target = id
		return id, true
	}
	draft := func(m *matrix, _ mxRole) (string, bool) {
		return m.store("drafter@x.de", map[string]any{"space": space, "draft": true}), true
	}
	archived := func(m *matrix, _ mxRole) (string, bool) {
		id := m.store("writer@x.de", map[string]any{"space": space})
		m.archive("writer@x.de", id)
		return id, true
	}
	return []mxOp{
		{name: "approved entry", tool: "whats_new", min: rkReader, prep: foreign(space), args: news(nil), seen: listed, hidden: rkReader, ghost: ghost},
		{name: "version of an updated entry", tool: "whats_new", min: rkReader, prep: updated, args: news(nil), seen: listed, check: version(2)},
		{name: "foreign draft is no news", tool: "whats_new", min: never, prep: draft, args: news(nil), seen: listed},
		{name: "count of entries waiting for review", tool: "whats_new", min: rkCurator, prep: draft, args: news(nil), seen: waiting},
		{name: "archived entry is no news", tool: "whats_new", min: never, prep: archived, args: news(nil), seen: listed},
	}
}

func TestPermissionMatrixNews(t *testing.T) {
	m := newMatrix(t)
	m.run("open space", newsOps(m, "open"), mxRoles)
	m.run("approval space", newsOps(m, "gated"), mxRoles)
}

// Personal spaces are left out of the news, even for their owner.
func TestPermissionMatrixNewsPersonalSpace(t *testing.T) {
	m := newMatrix(t)
	mine := func(m *matrix, _ mxRole) (string, bool) { return m.store("owner@x.de", nil), true }
	ops := []mxOp{{name: "personal entry", tool: "whats_new", min: never, prep: mine, args: func(string) map[string]any { return nil },
		seen: func(out map[string]any, id string) bool { return containsID(out["entries"], id) }}}
	m.run("personal space", ops, append([]mxRole{{"owner", "owner@x.de", rkOwner}}, mxRoles...))
}

// An update with a wrong version number tells the current version only to somebody who may
// change the entry; everybody else gets the refusal of their right, without the number.
func TestPermissionMatrixVersionConflictDoesNotLeak(t *testing.T) {
	m := newMatrix(t)
	for _, r := range mxRoles {
		t.Run(r.name, func(t *testing.T) {
			m.t = t
			id := m.store("writer@x.de", map[string]any{"space": "open"})
			m.e.mustCall(t, m.as("writer@x.de"), "update_knowledge", map[string]any{"id": id, "title": "v2", "expected_version": 1})
			m.e.mustCall(t, m.as("writer@x.de"), "update_knowledge", map[string]any{"id": id, "title": "v3", "expected_version": 2})
			_, msg := m.callTool(t, r.email, "update_knowledge", map[string]any{"id": id, "title": "x", "expected_version": 1})
			if msg == "" {
				t.Fatal("an update with an outdated version went through")
			}
			names := strings.Contains(msg, "3")
			if mayChange := r.rank >= rkCurator; names != mayChange {
				t.Fatalf("the answer names the current version = %v, only holders of the right may learn it (%v): %q", names, mayChange, msg)
			}
		})
	}
}
