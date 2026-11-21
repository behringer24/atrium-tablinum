package authz

import (
	"math/rand"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

// node is a team space of a test tree: its own membership role of the user, its default role and
// whether it stops inheriting.
func node(parent *spaceRow, member, def Role, noInherit bool) spaceRow {
	row := spaceRow{ID: uuid.New(), MemberRole: member, DefaultRole: def, NoInherit: noInherit}
	if parent != nil {
		id := parent.ID
		row.Parent = &id
	}
	return row
}

func TestComputeHierarchy(t *testing.T) {
	user := users.User{ID: uuid.New(), Active: true}

	root := func(member Role) spaceRow { return node(nil, member, None, false) }
	child := func(p spaceRow, member Role) spaceRow { return node(&p, member, None, false) }

	// chain builds root, child, grandchild with the given own roles.
	chain := func(r, c, g Role) []spaceRow {
		a := root(r)
		b := child(a, c)
		return []spaceRow{a, b, child(b, g)}
	}

	cases := []struct {
		name string
		rows []spaceRow
		want [3]Role // role in root, child, grandchild
	}{
		{"entry only in the grandchild", chain(None, None, Author), [3]Role{None, None, Author}},
		{"entry only in the child", chain(None, Reader, None), [3]Role{None, Reader, Reader}},
		{"parent role flows down", chain(Author, None, None), [3]Role{Author, Author, Author}},
		{"explicit entry lowers the inherited role", chain(Author, Reader, None), [3]Role{Author, Reader, Reader}},
		{"explicit entry raises the inherited role", chain(Reader, Curator, None), [3]Role{Reader, Curator, Curator}},
		{"nearest entry beats a higher one further up", chain(Admin, Reader, Author), [3]Role{Admin, Reader, Author}},
		{"the grandchild skips a child without entry", chain(Curator, None, None), [3]Role{Curator, Curator, Curator}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compute(user, tc.rows)
			for i, row := range tc.rows {
				if got.Role(row.ID) != tc.want[i] {
					t.Errorf("level %d: role = %v, expected %v", i, got.Role(row.ID), tc.want[i])
				}
			}
		})
	}

	t.Run("NoInherit stops the lookup", func(t *testing.T) {
		a := root(Admin)
		b := node(&a, None, None, true)
		got := compute(user, []spaceRow{a, b})
		if got.Role(a.ID) != Admin || got.Role(b.ID) != None {
			t.Errorf("roles: root %v, child %v", got.Role(a.ID), got.Role(b.ID))
		}
	})
	t.Run("NoInherit with an own entry keeps the own entry", func(t *testing.T) {
		a := root(Admin)
		b := node(&a, Reader, None, true)
		if got := compute(user, []spaceRow{a, b}).Role(b.ID); got != Reader {
			t.Errorf("role = %v", got)
		}
	})
	t.Run("NoInherit in the middle cuts off the grandchild too", func(t *testing.T) {
		a := root(Admin)
		b := node(&a, None, None, true)
		c := child(b, None)
		got := compute(user, []spaceRow{a, b, c})
		if got.Role(b.ID) != None || got.Role(c.ID) != None {
			t.Errorf("child %v, grandchild %v", got.Role(b.ID), got.Role(c.ID))
		}
	})
	t.Run("the default role is not inherited", func(t *testing.T) {
		a := node(nil, None, Author, false)
		b := child(a, None)
		got := compute(user, []spaceRow{a, b})
		if got.Role(a.ID) != Author || got.Role(b.ID) != None {
			t.Errorf("root %v, child %v", got.Role(a.ID), got.Role(b.ID))
		}
	})
	t.Run("the default role still counts next to an inherited one", func(t *testing.T) {
		a := root(Reader)
		b := node(&a, None, Author, false)
		if got := compute(user, []spaceRow{a, b}).Role(b.ID); got != Author {
			t.Errorf("role = %v", got)
		}
	})
	t.Run("an archived parent still passes memberships on", func(t *testing.T) {
		a := root(Author)
		a.Archived = true
		b := child(a, None)
		got := compute(user, []spaceRow{a, b})
		if got.Role(a.ID) != None {
			t.Errorf("an archived team space must stay invisible, got %v", got.Role(a.ID))
		}
		if got.Role(b.ID) != Author {
			t.Errorf("child role = %v, expected author", got.Role(b.ID))
		}
	})
	t.Run("an archived child stays invisible", func(t *testing.T) {
		a := root(Author)
		b := child(a, None)
		b.Archived = true
		if got := compute(user, []spaceRow{a, b}); got.Can(b.ID, Reader) {
			t.Error("archived child visible")
		}
	})
	t.Run("a missing parent ends the walk", func(t *testing.T) {
		ghost := spaceRow{ID: uuid.New()}
		b := child(ghost, None)
		if got := compute(user, []spaceRow{b}).Role(b.ID); got != None {
			t.Errorf("role = %v", got)
		}
	})
	t.Run("a personal space is never a source of inheritance", func(t *testing.T) {
		owner := user.ID
		p := spaceRow{ID: uuid.New(), Personal: true, Owner: &owner}
		b := child(p, None)
		got := compute(user, []spaceRow{p, b})
		if got.Role(p.ID) != Admin {
			t.Errorf("own personal space: %v", got.Role(p.ID))
		}
		if got.Role(b.ID) != None {
			t.Errorf("child of a personal space: %v", got.Role(b.ID))
		}
	})
	t.Run("a cycle ends and still honours entries on the loop", func(t *testing.T) {
		a := root(None)
		b := child(a, None)
		bid := b.ID
		a.Parent = &bid
		if got := compute(user, []spaceRow{a, b}); got.Can(a.ID, Reader) || got.Can(b.ID, Reader) {
			t.Errorf("no entry anywhere must stay no access: %v", got)
		}
		b.MemberRole = Reader
		got := compute(user, []spaceRow{a, b})
		if got.Role(a.ID) != Reader || got.Role(b.ID) != Reader {
			t.Errorf("roles: a %v, b %v", got.Role(a.ID), got.Role(b.ID))
		}
	})
	t.Run("an instance admin is admin everywhere", func(t *testing.T) {
		admin := users.User{ID: user.ID, Active: true, InstanceAdmin: true}
		a := root(None)
		b := node(&a, None, None, true)
		got := compute(admin, []spaceRow{a, b})
		if got.Role(a.ID) != Admin || got.Role(b.ID) != Admin {
			t.Errorf("roles: %v", got)
		}
	})
	t.Run("a disabled user has nothing", func(t *testing.T) {
		off := users.User{ID: user.ID, Active: false}
		a := root(Admin)
		if got := compute(off, []spaceRow{a, child(a, None)}); len(got) != 0 {
			t.Errorf("roles: %v", got)
		}
	})
}

// legacyCompute is the flat rule as it was before spaces had parents. A flat instance must keep
// getting exactly these roles.
func legacyCompute(u users.User, rows []spaceRow) Roles {
	roles := Roles{}
	if !u.Active {
		return roles
	}
	for _, s := range rows {
		if s.Personal {
			if s.Owner != nil && *s.Owner == u.ID && !s.Archived {
				roles[s.ID] = Admin
			}
			continue
		}
		if s.Archived {
			if u.InstanceAdmin {
				roles[s.ID] = Admin
			}
			continue
		}
		role := max(s.MemberRole, s.DefaultRole)
		if u.InstanceAdmin {
			role = Admin
		}
		if role > None {
			roles[s.ID] = role
		}
	}
	return roles
}

// Without a parent anywhere the result must not depend on the hierarchy at all, whatever the
// NoInherit flags say.
func TestComputeFlatMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	roleOf := func() Role { return Role(rng.Intn(int(Admin) + 1)) }
	for i := 0; i < 500; i++ {
		me := uuid.New()
		u := users.User{ID: me, Active: rng.Intn(8) > 0, InstanceAdmin: rng.Intn(4) == 0}
		var rows []spaceRow
		for j := rng.Intn(12); j >= 0; j-- {
			row := spaceRow{ID: uuid.New(), Archived: rng.Intn(5) == 0, NoInherit: rng.Intn(2) == 0}
			if rng.Intn(4) == 0 {
				owner := me
				if rng.Intn(2) == 0 {
					owner = uuid.New()
				}
				row.Personal, row.Owner = true, &owner
			} else {
				row.MemberRole, row.DefaultRole = roleOf(), roleOf()
			}
			rows = append(rows, row)
		}
		got, want := compute(u, rows), legacyCompute(u, rows)
		if len(got) != len(want) {
			t.Fatalf("run %d: %v, expected %v", i, got, want)
		}
		for id, r := range want {
			if got[id] != r {
				t.Fatalf("run %d: space %v role %v, expected %v", i, id, got[id], r)
			}
		}
	}
}
