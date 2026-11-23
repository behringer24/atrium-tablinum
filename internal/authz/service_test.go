package authz

import (
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestComputeServiceAccount(t *testing.T) {
	svc := users.User{ID: uuid.New(), Active: true, Kind: users.KindService}
	person := users.User{ID: svc.ID, Active: true}

	team := func(member, def Role) spaceRow {
		return spaceRow{ID: uuid.New(), MemberRole: member, DefaultRole: def}
	}

	cases := []struct {
		name string
		row  spaceRow
		svc  Role // role of the service account
		per  Role // role of a person with the same memberships
	}{
		{"default reader does not apply", team(None, Reader), None, Reader},
		{"default author does not apply", team(None, Author), None, Author},
		{"an explicit membership counts", team(Author, None), Author, Author},
		{"the highest role is the cap", team(Admin, None), ServiceMaxRole, Admin},
		{"the cap holds next to a default role", team(Admin, Reader), ServiceMaxRole, Admin},
		{"a role at the cap stays", team(ServiceMaxRole, None), ServiceMaxRole, ServiceMaxRole},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compute(svc, []spaceRow{tc.row}).Role(tc.row.ID); got != tc.svc {
				t.Errorf("service account: role = %v, expected %v", got, tc.svc)
			}
			if got := compute(person, []spaceRow{tc.row}).Role(tc.row.ID); got != tc.per {
				t.Errorf("person: role = %v, expected %v", got, tc.per)
			}
		})
	}

	t.Run("an inherited role is capped too", func(t *testing.T) {
		top := team(Admin, Reader)
		sub := spaceRow{ID: uuid.New(), Parent: &top.ID}
		got := compute(svc, []spaceRow{top, sub})
		if got.Role(top.ID) != ServiceMaxRole || got.Role(sub.ID) != ServiceMaxRole {
			t.Errorf("roles: top %v, sub %v", got.Role(top.ID), got.Role(sub.ID))
		}
	})
	t.Run("a service account never reads a personal space or a foreign one", func(t *testing.T) {
		other := uuid.New()
		own := spaceRow{ID: uuid.New(), Personal: true, Owner: &other}
		if compute(svc, []spaceRow{own}).Can(own.ID, Reader) {
			t.Error("service account in a foreign personal space")
		}
	})
	t.Run("a service account that is flagged as an instance admin is not one", func(t *testing.T) {
		// The users package never sets the flag for a service account; the computation must not rely on it
		// to keep the cap.
		flagged := users.User{ID: uuid.New(), Active: true, Kind: users.KindService, InstanceAdmin: true}
		row := team(None, None)
		if got := compute(flagged, []spaceRow{row}).Role(row.ID); got != ServiceMaxRole {
			t.Errorf("role = %v, expected the cap %v", got, ServiceMaxRole)
		}
	})
	t.Run("a disabled service account has nothing", func(t *testing.T) {
		off := users.User{ID: uuid.New(), Active: false, Kind: users.KindService}
		row := team(Admin, Author)
		if got := compute(off, []spaceRow{row}); len(got) != 0 {
			t.Errorf("roles: %v", got)
		}
	})
}
