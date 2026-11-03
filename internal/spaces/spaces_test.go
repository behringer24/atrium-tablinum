package spaces_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestCreateTeamValidation(t *testing.T) {
	ctx := context.Background()
	s := spaces.New(dbtest.NewPool(t))
	for _, p := range []spaces.CreateParams{
		{Slug: "A", Name: "x"},            // becomes "a": too short
		{Slug: "ok name", Name: "x"},      // whitespace
		{Slug: "personal-123", Name: "x"}, // reserved
		{Slug: "valid", Name: " "},        // empty name
		{Slug: "valid", Name: "x", DefaultRole: authz.Curator},
	} {
		if _, err := s.CreateTeam(ctx, p); !errors.Is(err, spaces.ErrInvalid) {
			t.Errorf("%+v: err = %v", p, err)
		}
	}
	if _, err := s.CreateTeam(ctx, spaces.CreateParams{Slug: "support", Name: "Support"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTeam(ctx, spaces.CreateParams{Slug: "Support", Name: "Doppelt"}); !errors.Is(err, spaces.ErrSlugTaken) {
		t.Errorf("Duplikat: %v", err)
	}
}

func TestUpdateAndDefaultRole(t *testing.T) {
	ctx := context.Background()
	s := spaces.New(dbtest.NewPool(t))
	sp, _ := s.CreateTeam(ctx, spaces.CreateParams{Slug: "firma", Name: "Firma", DefaultRole: authz.Reader})
	if sp.DefaultRole != authz.Reader {
		t.Fatalf("default_role = %v", sp.DefaultRole)
	}
	name, yes, none := "Firmenweit", true, authz.None
	up, err := s.Update(ctx, sp.ID, spaces.UpdateParams{Name: &name, RequireApproval: &yes, DefaultRole: &none})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != name || !up.RequireApproval || up.DefaultRole != authz.None || up.Description != "" {
		t.Errorf("%+v", up)
	}
	// without DefaultRole the value is kept
	author := authz.Author
	if _, err := s.Update(ctx, sp.ID, spaces.UpdateParams{DefaultRole: &author}); err != nil {
		t.Fatal(err)
	}
	desc := "neu"
	up, _ = s.Update(ctx, sp.ID, spaces.UpdateParams{Description: &desc})
	if up.DefaultRole != authz.Author || up.Description != "neu" {
		t.Errorf("%+v", up)
	}
	if _, err := s.Update(ctx, uuid.New(), spaces.UpdateParams{Name: &name}); !errors.Is(err, spaces.ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
}

func TestPersonalSpacesAreNotManageable(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	us, ss := users.New(pool, users.Options{PersonalEnabled: true}), spaces.New(pool)
	u, _ := us.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"})
	pid := *u.PersonalSpaceID
	n := "x"
	if _, err := ss.Update(ctx, pid, spaces.UpdateParams{Name: &n}); !errors.Is(err, spaces.ErrNotTeam) {
		t.Errorf("Update: %v", err)
	}
	if err := ss.SetArchived(ctx, pid, true); !errors.Is(err, spaces.ErrNotTeam) {
		t.Errorf("Archive: %v", err)
	}
	if err := ss.SetMember(ctx, pid, spaces.PrincipalUser, u.ID, authz.Reader, u.ID); !errors.Is(err, spaces.ErrNotTeam) {
		t.Errorf("SetMember: %v", err)
	}
}

func TestMembers(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	us, ss := users.New(pool, users.Options{PersonalEnabled: true}), spaces.New(pool)
	u, _ := us.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"})
	g, _ := us.CreateGroup(ctx, "Support")
	sp, _ := ss.CreateTeam(ctx, spaces.CreateParams{Slug: "support", Name: "Support"})

	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, u.ID, authz.Author, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalGroup, g.ID, authz.Reader, u.ID); err != nil {
		t.Fatal(err)
	}
	// change the role instead of creating a duplicate
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, u.ID, authz.Curator, u.ID); err != nil {
		t.Fatal(err)
	}
	ms, err := ss.Members(ctx, sp.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %v", ms, err)
	}
	for _, m := range ms {
		switch m.PrincipalType {
		case spaces.PrincipalUser:
			if m.Name != "a@x.de" || m.Role != authz.Curator {
				t.Errorf("user: %+v", m)
			}
		case spaces.PrincipalGroup:
			if m.Name != "Support" || m.Role != authz.Reader {
				t.Errorf("group: %+v", m)
			}
		}
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, uuid.New(), authz.Reader, u.ID); !errors.Is(err, spaces.ErrNotFound) {
		t.Errorf("unbekannter Nutzer: %v", err)
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalGroup, u.ID, authz.Reader, u.ID); !errors.Is(err, spaces.ErrNotFound) {
		t.Errorf("Nutzer-ID als Gruppe: %v", err)
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, u.ID, authz.None, u.ID); !errors.Is(err, spaces.ErrInvalid) {
		t.Errorf("ohne Rolle: %v", err)
	}
	if err := ss.RemoveMember(ctx, sp.ID, spaces.PrincipalGroup, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := ss.RemoveMember(ctx, sp.ID, spaces.PrincipalGroup, g.ID); !errors.Is(err, spaces.ErrNotFound) {
		t.Errorf("zweites Entfernen: %v", err)
	}
}
