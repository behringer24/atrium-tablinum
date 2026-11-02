package authz

import (
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestRoleOrderAndParse(t *testing.T) {
	if !(None < Reader && Reader < Author && Author < Curator && Curator < Admin) {
		t.Fatal("Rangfolge verletzt")
	}
	for _, s := range []string{"reader", "author", "curator", "admin"} {
		r, ok := ParseRole(s)
		if !ok || r.String() != s {
			t.Errorf("ParseRole(%q) = %v, %v", s, r, ok)
		}
	}
	for _, s := range []string{"", "root", "Admin"} {
		if _, ok := ParseRole(s); ok {
			t.Errorf("ParseRole(%q) darf nicht gelingen", s)
		}
	}
}

func TestRolesCan(t *testing.T) {
	a := uuid.New()
	r := Roles{a: Author}
	if !r.Can(a, Reader) || !r.Can(a, Author) || r.Can(a, Curator) {
		t.Error("Can falsch")
	}
	if r.Can(uuid.New(), Reader) {
		t.Error("unbekannter Bereich darf nichts erlauben")
	}
	if r.Can(a, None) {
		t.Error("None als Mindestrolle darf nie erlauben")
	}
	if !r.Any(Author) || r.Any(Admin) {
		t.Error("Any falsch")
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
		{"kein Zugriff", user, team(None, None, false), None},
		{"Mitglied", user, team(Curator, None, false), Curator},
		{"default reader", user, team(None, Reader, false), Reader},
		{"default author", user, team(None, Author, false), Author},
		{"höchste gewinnt: Mitglied über default", user, team(Admin, Reader, false), Admin},
		{"höchste gewinnt: default über Mitglied", user, team(Reader, Author, false), Author},
		{"archiviert für Nutzer unsichtbar", user, team(Admin, Reader, true), None},
		{"archiviert für Instanz-Admin", admin, team(None, None, true), Admin},
		{"Instanz-Admin auf Team", admin, team(None, None, false), Admin},
		{"eigener persönlicher Bereich", user, personal(me), Admin},
		{"fremder persönlicher Bereich", user, personal(other), None},
		{"Instanz-Admin nie in fremdem persönlichen Bereich", admin, personal(other), None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compute(tc.u, []spaceRow{tc.row}).Role(tc.row.ID)
			if got != tc.want {
				t.Errorf("Rolle = %v, erwartet %v", got, tc.want)
			}
		})
	}

	t.Run("Mitgliedschaft in persönlichem Bereich wird ignoriert", func(t *testing.T) {
		row := personal(other)
		row.MemberRole, row.DefaultRole = Admin, Author
		if compute(user, []spaceRow{row}).Can(row.ID, Reader) {
			t.Error("Zugriff auf fremden persönlichen Bereich")
		}
	})
	t.Run("deaktivierter Nutzer hat nichts", func(t *testing.T) {
		u := users.User{ID: me, Active: false, InstanceAdmin: true}
		row := team(Admin, Author, false)
		own := personal(me)
		if got := compute(u, []spaceRow{row, own}); len(got) != 0 {
			t.Errorf("Rollen: %v", got)
		}
	})
}
