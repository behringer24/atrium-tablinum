package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/allowlist"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/reembed"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// The permission matrix for the administration tools (see permission_matrix_test.go for the
// method). The admin_* tools are for instance admins alone: a space admin gets none of them. The
// tools of space admins (space_members, space_add_member, space_remove_member) work on the spaces
// they administer and on no other; an instance admin administers all.

// adminServices gives the server the services the admin tools need to answer at all; without them
// a tool refuses with "not managed by this server", which would hide a missing right behind a
// missing service.
func adminServices(m *matrix) {
	m.e.srv.Allowlist = allowlist.New(m.e.srv.Pool)
	m.e.srv.Reembed = reembed.New(m.e.srv.Pool, fakeEmbedder{})
	m.e.srv.RevokeTokens = func(context.Context, uuid.UUID) (int64, error) { return 0, nil }
	m.e.srv.RevokeAllTokens = func(context.Context) (int64, error) { return 0, nil }
}

// freshUser is an account that nothing else in the matrix uses.
func (m *matrix) freshUser() string {
	m.t.Helper()
	m.n++
	email := fmt.Sprintf("fresh%d@x.de", m.n)
	if _, err := m.e.users.Provision(m.e.ctx, users.Identity{EmailVerified: true, IDP: "t", Subject: email, Email: email}); err != nil {
		m.t.Fatal(err)
	}
	return email
}

// freshSpace is a team space that nothing else in the matrix uses; archived on request.
func (m *matrix) freshSpace(archived bool) string {
	m.t.Helper()
	m.n++
	slug := fmt.Sprintf("fresh-space-%d", m.n)
	sp, err := m.e.spaces.CreateTeam(m.e.ctx, spaces.CreateParams{Slug: slug, Name: slug})
	if err != nil {
		m.t.Fatal(err)
	}
	if archived {
		if err := m.e.spaces.SetArchived(m.e.ctx, sp.ID, true); err != nil {
			m.t.Fatal(err)
		}
	}
	return slug
}

func (m *matrix) uniq(prefix string) string {
	m.n++
	return fmt.Sprintf("%s-%d", prefix, m.n)
}

func fixed(a map[string]any) func(string) map[string]any {
	return func(string) map[string]any { return a }
}

func adminOps(m *matrix) []mxOp {
	none := func(*matrix, mxRole) (string, bool) { return "", true }
	op := func(name, tool string, args func(id string) map[string]any) mxOp {
		return mxOp{name: name, tool: tool, min: rkInstance, prep: none, args: args}
	}
	withUser := func(name, tool string, extra map[string]any) mxOp {
		o := op(name, tool, nil)
		o.prep = func(m *matrix, _ mxRole) (string, bool) { return m.freshUser(), true }
		o.args = func(email string) map[string]any {
			a := map[string]any{"user_email": email}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
		return o
	}
	withSpace := func(name, tool string, archived bool, extra map[string]any) mxOp {
		o := op(name, tool, nil)
		o.prep = func(m *matrix, _ mxRole) (string, bool) { return m.freshSpace(archived), true }
		o.args = func(slug string) map[string]any {
			a := map[string]any{"space": slug}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
		return o
	}
	confirm := map[string]any{"confirm": true}
	offboarded := withUser("reactivate user", "admin_reactivate_user", nil)
	offboarded.prep = func(m *matrix, _ mxRole) (string, bool) {
		email := m.freshUser()
		m.skillAs("boss@x.de", "admin_offboard_user", map[string]any{"user_email": email, "confirm": true})
		return email, true
	}
	newSpace := op("create space", "admin_create_space", func(string) map[string]any {
		slug := m.uniq("made-space")
		return map[string]any{"slug": slug, "name": slug}
	})
	return []mxOp{
		newSpace,
		withSpace("update space", "admin_update_space", false, map[string]any{"require_approval": true}),
		withSpace("archive space", "admin_archive_space", false, confirm),
		withSpace("restore space", "admin_restore_space", true, nil),
		op("list users", "admin_list_users", fixed(nil)),
		withUser("offboard user", "admin_offboard_user", confirm),
		offboarded,
		op("list groups", "admin_groups", fixed(map[string]any{"action": "list"})),
		op("create group", "admin_groups", func(string) map[string]any {
			return map[string]any{"action": "create", "name": m.uniq("group")}
		}),
		withUser("end the sessions of a user", "admin_revoke_sessions", confirm),
		op("end all sessions", "admin_revoke_all_sessions", fixed(confirm)),
		op("list the allowlist", "admin_allowlist", fixed(map[string]any{"action": "list"})),
		op("read the context", "admin_context", fixed(map[string]any{"action": "get"})),
		op("set the context", "admin_context", fixed(map[string]any{"action": "set", "text": "Test context.", "confirm": true})),
		op("read the instance names", "admin_instance", fixed(map[string]any{"action": "get"})),
		op("set the instance names", "admin_instance", fixed(map[string]any{"action": "set", "short_name": "Matrix", "confirm": true})),
		op("read the quotas", "admin_quotas", fixed(map[string]any{"action": "get"})),
		op("set a quota", "admin_quotas", fixed(map[string]any{"action": "set", "scope": "team_default", "documents": 1000, "confirm": true})),
		op("read the audit log", "admin_audit_log", fixed(nil)),
		op("read the health", "admin_health", fixed(nil)),
		op("read the support bundle", "admin_support_bundle", fixed(nil)),
		op("read the re-embedding status", "admin_reembed", fixed(map[string]any{"action": "status"})),
	}
}

func TestPermissionMatrixAdminTools(t *testing.T) {
	m := newMatrix(t)
	adminServices(m)
	m.run("admin tools", adminOps(m), mxRoles)
}

// The tools of space admins reach the spaces they administer, and no other.
func TestPermissionMatrixSpaceAdminTools(t *testing.T) {
	m := newMatrix(t)
	// A space where the space admin of the other spaces is a mere reader, and somebody else is admin.
	m.e.team(t, "ops", "Operations", false, map[string]authz.Role{
		"reader@x.de": authz.Reader, "author@x.de": authz.Author, "curator@x.de": authz.Curator,
		"spaceadmin@x.de": authz.Reader, "writer@x.de": authz.Admin,
	})
	old := m.e.team(t, "old", "Old", false, map[string]authz.Role{"spaceadmin@x.de": authz.Admin, "writer@x.de": authz.Curator})
	if err := m.e.spaces.SetArchived(m.e.ctx, old.ID, true); err != nil {
		t.Fatal(err)
	}
	// where the admin of the "open" spaces administers, and where not
	adminOfOpen := func(r mxRole) bool { return r.rank >= rkAdmin }
	adminOfOps := func(r mxRole) bool { return r.rank >= rkInstance }
	opsFor := func(space string, allow func(mxRole) bool, hidden int) []mxOp {
		member := func(m *matrix, _ mxRole) (string, bool) { return m.freshUser(), true }
		added := func(m *matrix, r mxRole) (string, bool) {
			email := m.freshUser()
			m.skillAs("boss@x.de", "space_add_member", map[string]any{"space": space, "user_email": email, "role": "reader"})
			return email, true
		}
		return []mxOp{
			{name: "show members", tool: "space_members", prep: func(*matrix, mxRole) (string, bool) { return "", true },
				args: fixed(map[string]any{"space": space}), allow: allow, hidden: hidden,
				ghost: map[string]any{"space": "no-such-space"}},
			{name: "add a member", tool: "space_add_member", prep: member, allow: allow, hidden: hidden,
				args: func(email string) map[string]any {
					return map[string]any{"space": space, "user_email": email, "role": "reader"}
				},
				ghost: map[string]any{"space": "no-such-space", "user_email": "reader@x.de", "role": "reader"}},
			{name: "add a member as admin", tool: "space_add_member", prep: member, allow: allow, hidden: hidden,
				args: func(email string) map[string]any {
					return map[string]any{"space": space, "user_email": email, "role": "admin"}
				},
				ghost: map[string]any{"space": "no-such-space", "user_email": "reader@x.de", "role": "admin"}},
			{name: "remove a member", tool: "space_remove_member", prep: added, allow: allow, hidden: hidden,
				args: func(email string) map[string]any {
					return map[string]any{"space": space, "user_email": email, "confirm": true}
				},
				ghost: map[string]any{"space": "no-such-space", "user_email": "reader@x.de", "confirm": true}},
		}
	}
	m.run("space admin tools, own space", opsFor("open", adminOfOpen, 0), mxRoles)
	m.run("space admin tools, foreign space", opsFor("ops", adminOfOps, rkInstance), mxRoles)
	m.run("space admin tools, archived space", opsFor("old", adminOfOps, rkInstance), mxRoles)
}
