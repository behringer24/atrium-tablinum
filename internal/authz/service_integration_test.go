package authz_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// A service account loaded from the database gets only what it was given, capped, and the default role
// of an open space does not reach it.
func TestServiceAccountRolesFromDatabase(t *testing.T) {
	e := setup(t, time.Minute)
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx,
		`INSERT INTO users (email, idp, idp_subject, kind) VALUES ('docs@service.invalid', 'service', 'docs', 'service') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc, err := e.users.Get(e.ctx, id)
	if err != nil || !svc.IsService() {
		t.Fatalf("service account: %+v %v", svc, err)
	}
	anna := e.user(t, "anna@x.de")

	open := e.team(t, "offen", authz.Author) // every logged-in person may write
	given := e.team(t, "handbuch", authz.None)
	admin := e.team(t, "intern", authz.None)
	other := e.team(t, "geheim", authz.None)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.spaces.SetMember(e.ctx, given.ID, spaces.PrincipalUser, svc.ID, authz.Author, anna.ID))
	must(e.spaces.SetMember(e.ctx, admin.ID, spaces.PrincipalUser, svc.ID, authz.Admin, anna.ID))

	r := e.roles(t, svc)
	if r.Role(open.ID) != authz.None {
		t.Errorf("open space: %v, the default role must not apply", r.Role(open.ID))
	}
	if r.Role(given.ID) != authz.Author {
		t.Errorf("given space: %v", r.Role(given.ID))
	}
	if r.Role(admin.ID) != authz.ServiceMaxRole {
		t.Errorf("space with an admin membership: %v, expected the cap %v", r.Role(admin.ID), authz.ServiceMaxRole)
	}
	if r.Can(other.ID, authz.Reader) {
		t.Error("a space it was not given")
	}
	if r.Can(open.ID, authz.Admin) || r.Any(authz.Admin) {
		t.Error("a service account holds the admin role somewhere")
	}

	// A person next to it still gets the default role of the open space.
	if got := e.roles(t, anna).Role(open.ID); got != authz.Author {
		t.Errorf("person in the open space: %v", got)
	}
}
