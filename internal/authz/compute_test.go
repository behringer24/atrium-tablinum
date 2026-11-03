package authz

import (
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestRoleOrderAndParse(t *testing.T) {
	if !(None < Reader && Reader < Author && Author < Curator && Curator < Admin) {
		t.Fatal("ranking violated")
	}
	for _, s := range []string{"reader", "author", "curator", "admin"} {
		r, ok := ParseRole(s)
		if !ok || r.String() != s {
			t.Errorf("ParseRole(%q) = %v, %v", s, r, ok)
		}
	}
	for _, s := range []string{"", "root", "Admin"} {
		if _, ok := ParseRole(s); ok {
			t.Errorf("ParseRole(%q) must not succeed", s)
		}
	}
}

func TestRolesCan(t *testing.T) {
	a := uuid.New()
	r := Roles{a: Author}
	if !r.Can(a, Reader) || !r.Can(a, Author) || r.Can(a, Curator) {
		t.Error("Can wrong")
	}
	if r.Can(uuid.New(), Reader) {
		t.Error("unknown space must not allow anything")
	}
	if r.Can(a, None) {
		t.Error("None as minimum role must never allow")
	}
	if !r.Any(Author) || r.Any(Admin) {
		t.Error("Any wrong")
	}
}

func TestCompute(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	user := users.User{ID: me, Active: true}
	admin := users.User{ID: me, Active: true, InstanceAdmin: true}

	team := func(member, def Role, archived bool) spaceRow {
		return spaceRow{ID: uuid.New(), MemberRole: member, DefaultRole: def, Archived: archived}
	}
	personal := func(owner uuid.UUID) spaceRow {
		return spaceRow{ID: uuid.New(), Personal: true, Owner: &owner}
	}

	cases := []struct {
		name string
		u    users.User
		row  spaceRow
		want Role
	}{
		{"no access", user, team(None, None, false), None},
		{"member", user, team(Curator, None, false), Curator},
		{"default reader", user, team(None, Reader, false), Reader},
		{"default author", user, team(None, Author, false), Author},
		{"highest wins: member over default", user, team(Admin, Reader, false), Admin},
		{"highest wins: default over member", user, team(Reader, Author, false), Author},
		{"archived invisible to user", user, team(Admin, Reader, true), None},
		{"archived for instance admin", admin, team(None, None, true), Admin},
		{"instance admin on team", admin, team(None, None, false), Admin},
		{"own personal space", user, personal(me), Admin},
		{"foreign personal space", user, personal(other), None},
		{"instance admin never in foreign personal space", admin, personal(other), None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compute(tc.u, []spaceRow{tc.row}).Role(tc.row.ID)
			if got != tc.want {
				t.Errorf("role = %v, expected %v", got, tc.want)
			}
		})
	}

	t.Run("membership in personal space is ignored", func(t *testing.T) {
		row := personal(other)
		row.MemberRole, row.DefaultRole = Admin, Author
		if compute(user, []spaceRow{row}).Can(row.ID, Reader) {
			t.Error("access to foreign personal space")
		}
	})
	t.Run("disabled user has nothing", func(t *testing.T) {
		u := users.User{ID: me, Active: false, InstanceAdmin: true}
		row := team(Admin, Author, false)
		own := personal(me)
		if got := compute(u, []spaceRow{row, own}); len(got) != 0 {
			t.Errorf("roles: %v", got)
		}
	})
}
