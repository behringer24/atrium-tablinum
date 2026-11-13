package mcpserver

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// The permission matrix: who may do what with knowledge, in which kind of space. Every case calls
// the real MCP tool as the real role and compares the outcome with the rule written down here, so
// a change to a right shows up as a failing cell instead of going unnoticed. The expectations
// come from docs/auth.md and the rules in internal/docs and internal/authz; they are written out
// by hand on purpose and do not call the code under test.

// rank orders the callers of a team space. The instance admin counts as admin of every team space
// but also sees what only instance admins see (archived spaces).
const (
	rkOutsider = iota // no membership, no default role
	rkReader
	rkAuthor
	rkCurator
	rkAdmin          // space admin
	rkInstance       // instance admin
	rkOwner    = 100 // owner of a personal space; only used for personal spaces
	never      = 1000
)

type mxRole struct {
	name  string
	email string
	rank  int
}

var mxRoles = []mxRole{
	{"outsider", "outsider@x.de", rkOutsider},
	{"reader", "reader@x.de", rkReader},
	{"author", "author@x.de", rkAuthor},
	{"curator", "curator@x.de", rkCurator},
	{"spaceadmin", "spaceadmin@x.de", rkAdmin},
	{"instanceadmin", "boss@x.de", rkInstance},
}

// mxOp is one tool call with the lowest rank that may do it. min is compared with the caller's
// rank; use never for "nobody". prep creates the entry the call works on (a fresh one for every
// case, because most calls change it) and reports false if this role cannot have such an entry.
type mxOp struct {
	name   string
	tool   string
	min    int
	prep   func(m *matrix, r mxRole) (id string, ok bool)
	args   func(id string) map[string]any
	ghost  map[string]any // call without an id: the same call against something that does not exist
	seen   func(out map[string]any, id string) bool
	check  func(r mxRole, out map[string]any) string
	hidden int // callers below this rank must not learn that the target exists
	// allow, if set, decides instead of min, for rights that do not follow the rank (a space
	// admin of another space).
	allow func(r mxRole) bool
}

type matrix struct {
	t    *testing.T
	e    *env
	sess map[string]*mcp.ClientSession
	n    int
}

func (m *matrix) as(email string) *mcp.ClientSession {
	if cs, ok := m.sess[email]; ok {
		return cs
	}
	cs := m.e.session(m.t, email)
	m.sess[email] = cs
	return cs
}

// store writes an entry as the given user and returns its id.
func (m *matrix) store(email string, args map[string]any) string {
	m.t.Helper()
	m.n++
	a := map[string]any{"title": fmt.Sprintf("mx entry %d", m.n), "content": fmt.Sprintf("mxtoken%d body text", m.n), "force": true}
	for k, v := range args {
		a[k] = v
	}
	out := m.e.mustCall(m.t, m.as(email), "store_knowledge", a)
	if out["result"] != "stored" {
		m.t.Fatalf("store as %s: %v", email, out)
	}
	return out["id"].(string)
}

// callTool is env.call for the matrix: a call that the server refuses at the protocol level, as it
// does for a tool the caller is not offered, counts as a refusal with the error as its message.
func (m *matrix) callTool(t *testing.T, email, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := m.as(email).CallTool(m.e.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, err.Error()
	}
	if res.IsError {
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return nil, sb.String()
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return out, ""
}

func (m *matrix) archive(email, id string) {
	m.t.Helper()
	m.e.mustCall(m.t, m.as(email), "archive_knowledge", map[string]any{"id": id, "confirm": true})
}

// foreign is an approved entry of somebody who takes part in none of the cases.
func foreign(space string) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, _ mxRole) (string, bool) {
		return m.store("writer@x.de", map[string]any{"space": space}), true
	}
}

// own is an entry written by the caller; callers who cannot write have none.
func own(space string, extra map[string]any) func(*matrix, mxRole) (string, bool) {
	return func(m *matrix, r mxRole) (string, bool) {
		if r.rank < rkAuthor {
			return "", false
		}
		args := map[string]any{"space": space}
		for k, v := range extra {
			args[k] = v
		}
		return m.store(r.email, args), true
	}
}

func idArgs(extra map[string]any) func(string) map[string]any {
	return func(id string) map[string]any {
		a := map[string]any{"id": id}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}
}

func knowledgeOps(space string) []mxOp {
	confirm := map[string]any{"confirm": true}
	ops := []mxOp{
		{name: "list", tool: "list_knowledge", min: rkReader, prep: foreign(space),
			args:  func(string) map[string]any { return map[string]any{"space": space} },
			ghost: map[string]any{"space": "no-such-space"},
			seen: func(out map[string]any, id string) bool {
				return containsID(out["entries"], id)
			}},
		{name: "search", tool: "search_knowledge", min: rkReader, prep: foreign(space),
			args: func(string) map[string]any { return nil }, // filled in by the runner (needs the token)
			seen: func(out map[string]any, id string) bool { return containsID(out["results"], id) }},
		{name: "get", tool: "get_knowledge", min: rkReader, prep: foreign(space), args: idArgs(nil), hidden: rkReader},
		{name: "store", tool: "store_knowledge", min: rkAuthor, seen: storedOK, hidden: rkReader,
			prep: func(*matrix, mxRole) (string, bool) { return "", true },
			args: func(string) map[string]any {
				return map[string]any{"title": "new", "content": "mxstore body", "space": space, "force": true}
			},
			ghost: map[string]any{"title": "new", "content": "mxstore body", "space": "no-such-space", "force": true},
			check: func(r mxRole, out map[string]any) string {
				want := "approved"
				if space == "gated" && r.rank < rkCurator {
					want = "draft"
				}
				if out["status"] != want {
					return fmt.Sprintf("status %v, want %s", out["status"], want)
				}
				return ""
			}},
		{name: "store a rule", tool: "store_knowledge", min: rkCurator, seen: storedOK, hidden: rkReader,
			prep: func(*matrix, mxRole) (string, bool) { return "", true },
			args: func(string) map[string]any {
				return map[string]any{"title": "rule", "content": "mxrule body", "space": space, "force": true, "type": "rule"}
			}},
		{name: "update foreign", tool: "update_knowledge", min: rkCurator, prep: foreign(space),
			args: idArgs(map[string]any{"title": "changed"}), hidden: rkReader},
		{name: "update own", tool: "update_knowledge", min: rkAuthor, prep: own(space, nil),
			args: idArgs(map[string]any{"title": "changed"})},
		{name: "archive foreign", tool: "archive_knowledge", min: rkCurator, prep: foreign(space), args: idArgs(confirm), hidden: rkReader},
		{name: "archive own", tool: "archive_knowledge", min: rkAuthor, prep: own(space, nil), args: idArgs(confirm)},
		{name: "unarchive foreign", tool: "unarchive_knowledge", min: rkCurator, hidden: rkReader,
			prep: func(m *matrix, _ mxRole) (string, bool) {
				id := m.store("writer@x.de", map[string]any{"space": space})
				m.archive("writer@x.de", id)
				return id, true
			}, args: idArgs(confirm)},
		{name: "move foreign", tool: "move_knowledge", seen: movedOK, min: rkCurator, prep: foreign(space),
			args: idArgs(map[string]any{"space": "dst", "confirm": true}), hidden: rkReader},
		{name: "delete foreign", tool: "delete_knowledge", min: rkAdmin, prep: foreign(space), args: idArgs(confirm), hidden: rkReader},
		{name: "delete own", tool: "delete_knowledge", min: rkAdmin, prep: own(space, nil), args: idArgs(confirm)},
		{name: "export", tool: "export_knowledge", min: rkAdmin,
			prep:  func(*matrix, mxRole) (string, bool) { return "", true },
			args:  func(string) map[string]any { return map[string]any{"space": space} },
			ghost: map[string]any{"space": "no-such-space"}},
	}
	if space == "open" {
		// An author moves their own approved entry; in a space with approval that is a curator's
		// decision (and an author has no approved entries of their own there).
		ops = append(ops, mxOp{name: "move own", tool: "move_knowledge", seen: movedOK, min: rkAuthor, prep: own(space, nil),
			args: idArgs(map[string]any{"space": "dst", "confirm": true})})
	}
	if space == "gated" {
		draft := func(m *matrix, _ mxRole) (string, bool) {
			return m.store("drafter@x.de", map[string]any{"space": space}), true
		}
		ownDraft := func(m *matrix, r mxRole) (string, bool) {
			if r.rank < rkCurator {
				return "", false // below curator an own entry is a draft anyway and nobody else reviews it here
			}
			return m.store(r.email, map[string]any{"space": space, "draft": true}), true
		}
		ops = append(ops,
			mxOp{name: "get foreign draft", tool: "get_knowledge", min: rkCurator, prep: draft, args: idArgs(nil), hidden: rkReader},
			mxOp{name: "approve draft", tool: "review_knowledge", min: rkCurator, prep: draft, hidden: rkReader,
				args: idArgs(map[string]any{"decision": "approve", "confirm": true})},
			mxOp{name: "return draft", tool: "review_knowledge", min: rkCurator, prep: draft, hidden: rkReader,
				args: idArgs(map[string]any{"decision": "reject", "note": "please rework"})},
			// Nobody approves their own entry, except a space admin (rule: internal/docs/review.go).
			mxOp{name: "approve own draft", tool: "review_knowledge", min: rkAdmin, prep: ownDraft,
				args: idArgs(map[string]any{"decision": "approve", "confirm": true})},
		)
	}
	return ops
}

// storedOK: a store call that finds no space to write to answers without an error, but stores nothing.
func storedOK(out map[string]any, _ string) bool { return out["result"] == "stored" }

// movedOK: a move to a space the caller cannot write to answers without an error, but moves nothing.
func movedOK(out map[string]any, _ string) bool { return out["result"] == "moved" }

var spaceName = regexp.MustCompile(`space "[^"]*"`)

// answer is what the caller learns from a call: the error text, or the result and message of a
// call that went through without storing anything.
func answer(out map[string]any, msg string) string {
	if msg == "" {
		msg = fmt.Sprintf("%v / %v", out["result"], out["message"])
	}
	// the answer repeats the name that the caller typed; that is not what is compared
	return spaceName.ReplaceAllString(msg, `space "X"`)
}

func containsID(list any, id string) bool {
	items, _ := list.([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["id"] == id {
			return true
		}
	}
	return false
}

// run executes every cell of one table for every role.
func (m *matrix) run(kind string, ops []mxOp, roles []mxRole) {
	outer := m.t
	defer func() { m.t = outer }()
	for _, op := range ops {
		for _, r := range roles {
			outer.Run(kind+"/"+op.name+"/"+r.name, func(t *testing.T) {
				m.t = t
				id, ok := op.prep(m, r)
				if !ok {
					return
				}
				args := op.args(id)
				if op.name == "search" {
					args = map[string]any{"query": fmt.Sprintf("mxtoken%d", m.n), "limit": 20}
				}
				out, msg := m.callTool(t, r.email, op.tool, args)
				allowed := msg == ""
				if allowed && op.seen != nil {
					allowed = op.seen(out, id)
				}
				want := r.rank >= op.min
				if op.allow != nil {
					want = op.allow(r)
				}
				if allowed != want {
					t.Fatalf("%s as %s: allowed=%v, the rule says %v (message %q)", op.name, r.name, allowed, want, msg)
				}
				if allowed && op.check != nil {
					if problem := op.check(r, out); problem != "" {
						t.Fatalf("%s as %s: %s", op.name, r.name, problem)
					}
				}
				// Whoever may not even see the target gets the same answer as for one that does not exist.
				if !allowed && r.rank < op.hidden {
					ghost := op.ghost
					if ghost == nil {
						ghost = op.args(uuid.New().String())
					}
					gout, gmsg := m.callTool(t, r.email, op.tool, ghost)
					if got, base := answer(out, msg), answer(gout, gmsg); got != base {
						t.Fatalf("%s as %s reveals that the target exists: %q, for a missing one: %q", op.name, r.name, got, base)
					}
				}
			})
		}
	}
}

func newMatrix(t *testing.T) *matrix {
	e := setup(t, 0)
	members := map[string]authz.Role{
		"reader@x.de": authz.Reader, "author@x.de": authz.Author, "curator@x.de": authz.Curator,
		"spaceadmin@x.de": authz.Admin, "writer@x.de": authz.Curator, "drafter@x.de": authz.Author,
	}
	e.team(t, "open", "Open", false, members)
	e.team(t, "gated", "Gated", true, members)
	e.team(t, "dst", "Destination", false, members)
	// the outsider and the owner of the personal space exist but belong to no team space
	m := &matrix{t: t, e: e, sess: map[string]*mcp.ClientSession{}}
	// All sessions are opened here: a session lives as long as the test that opened it.
	for _, email := range []string{"outsider@x.de", "owner@x.de", "boss@x.de", "writer@x.de", "drafter@x.de", "reader@x.de", "author@x.de", "curator@x.de", "spaceadmin@x.de"} {
		m.as(email)
	}
	return m
}

func TestPermissionMatrixKnowledge(t *testing.T) {
	m := newMatrix(t)
	m.run("open space", knowledgeOps("open"), mxRoles)
	m.run("approval space", knowledgeOps("gated"), mxRoles)
}

// A personal space belongs to its owner alone: not to members, not to instance admins.
func TestPermissionMatrixPersonalSpace(t *testing.T) {
	m := newMatrix(t)
	mine := func(m *matrix, _ mxRole) (string, bool) { return m.store("owner@x.de", nil), true }
	confirm := map[string]any{"confirm": true}
	ops := []mxOp{
		{name: "get", tool: "get_knowledge", min: rkOwner, prep: mine, args: idArgs(nil), hidden: rkOwner},
		{name: "search", tool: "search_knowledge", min: rkOwner, prep: mine, args: func(string) map[string]any { return nil },
			seen: func(out map[string]any, id string) bool { return containsID(out["results"], id) }},
		{name: "update", tool: "update_knowledge", min: rkOwner, prep: mine, args: idArgs(map[string]any{"title": "changed"}), hidden: rkOwner},
		{name: "archive", tool: "archive_knowledge", min: rkOwner, prep: mine, args: idArgs(confirm), hidden: rkOwner},
		{name: "delete", tool: "delete_knowledge", min: rkOwner, prep: mine, args: idArgs(confirm), hidden: rkOwner},
	}
	roles := append([]mxRole{{"owner", "owner@x.de", rkOwner}}, mxRoles...)
	m.run("personal space", ops, roles)
}

// An archived team space is visible to instance admins only, and nothing is written to it.
func TestPermissionMatrixArchivedSpace(t *testing.T) {
	m := newMatrix(t)
	old := m.e.team(t, "old", "Old", false, map[string]authz.Role{
		"reader@x.de": authz.Reader, "author@x.de": authz.Author, "curator@x.de": authz.Curator,
		"spaceadmin@x.de": authz.Admin, "writer@x.de": authz.Curator,
	})
	archiveAll := func() {
		if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	fresh := func(m *matrix, _ mxRole) (string, bool) {
		// Entries cannot be added to the archived space any more: take them from the stock made
		// before it was archived, and recreate the stock for the next case by restoring the space.
		if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, false); err != nil {
			t.Fatal(err)
		}
		id := m.store("writer@x.de", map[string]any{"space": "old"})
		archiveAll()
		return id, true
	}
	confirm := map[string]any{"confirm": true}
	ops := []mxOp{
		{name: "get", tool: "get_knowledge", min: rkInstance, prep: fresh, args: idArgs(nil), hidden: rkInstance},
		{name: "list", tool: "list_knowledge", min: rkInstance, prep: fresh,
			args:  func(string) map[string]any { return map[string]any{"space": "old"} },
			ghost: map[string]any{"space": "no-such-space"}, hidden: rkInstance,
			seen: func(out map[string]any, id string) bool { return containsID(out["entries"], id) }},
		{name: "search", tool: "search_knowledge", min: rkInstance, prep: fresh, args: func(string) map[string]any { return nil },
			seen: func(out map[string]any, id string) bool { return containsID(out["results"], id) }},
		{name: "store", tool: "store_knowledge", min: never, seen: storedOK, prep: func(*matrix, mxRole) (string, bool) { return "", true },
			args: func(string) map[string]any {
				return map[string]any{"title": "new", "content": "mxstore body", "space": "old", "force": true}
			},
			ghost: map[string]any{"title": "new", "content": "mxstore body", "space": "no-such-space", "force": true}, hidden: rkInstance},
		{name: "update", tool: "update_knowledge", min: never, prep: fresh, args: idArgs(map[string]any{"title": "changed"}), hidden: rkInstance},
		{name: "archive", tool: "archive_knowledge", min: never, prep: fresh, args: idArgs(confirm), hidden: rkInstance},
		{name: "delete", tool: "delete_knowledge", min: rkInstance, prep: fresh, args: idArgs(confirm), hidden: rkInstance},
	}
	m.run("archived space", ops, mxRoles)
}
