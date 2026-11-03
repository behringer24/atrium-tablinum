package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func newStore(t *testing.T, personal bool) *users.Store {
	t.Helper()
	return users.New(dbtest.NewPool(t), users.Options{BootstrapAdmins: []string{"Boss@x.de"}, PersonalEnabled: personal})
}

func TestProvisionCreatesUserAndPersonalSpaceOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, true)
	id := users.Identity{IDP: "google", Subject: "1", Email: "Anna@x.de", DisplayName: "Anna"}
	u1, err := s.Provision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u1.PersonalSpaceID == nil || !u1.Active || u1.InstanceAdmin {
		t.Fatalf("%+v", u1)
	}
	u2, err := s.Provision(ctx, id)
	if err != nil || u2.ID != u1.ID || *u2.PersonalSpaceID != *u1.PersonalSpaceID {
		t.Fatalf("zweiter Login: %+v %v", u2, err)
	}
	// A second IdP with the same e-mail does NOT take over the account by default
	if _, err := s.Provision(ctx, users.Identity{IDP: "entra", Subject: "zzz", Email: "anna@X.de"}); !errors.Is(err, users.ErrEmailTaken) {
		t.Fatalf("zweiter IdP ohne Verknüpfung: %v", err)
	}
	if all, _ := s.List(ctx); len(all) != 1 {
		t.Errorf("Nutzer = %d", len(all))
	}
}

func TestLinkByEmailIsOptInAndAudited(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	s := users.New(pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true, LinkByEmail: true})
	first, err := s.Provision(ctx, users.Identity{IDP: "google", Subject: "1", Email: "boss@x.de"})
	if err != nil {
		t.Fatal(err)
	}
	// E-mail matching is case-insensitive and then applies across IdPs
	linked, err := s.Provision(ctx, users.Identity{IDP: "entra", Subject: "zzz", Email: "BOSS@x.de"})
	if err != nil || linked.ID != first.ID {
		t.Fatalf("zweiter IdP: %+v %v", linked, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'account_linked' AND user_id = $1`, first.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("Audit account_linked: %d %v", n, err)
	}
	// A login with a new subject but a known e-mail goes through the e-mail path again;
	// what matters is that every case appears in the audit log.
	if _, err := s.Provision(ctx, users.Identity{IDP: "entra", Subject: "zzz", Email: "boss@x.de"}); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionWithoutPersonalSpaces(t *testing.T) {
	s := newStore(t, false)
	u, err := s.Provision(context.Background(), users.Identity{IDP: "g", Subject: "1", Email: "a@x.de"})
	if err != nil || u.PersonalSpaceID != nil {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestProvisionRejectsIncompleteIdentity(t *testing.T) {
	s := newStore(t, true)
	if _, err := s.Provision(context.Background(), users.Identity{IDP: "g", Subject: "1"}); err == nil {
		t.Fatal("ohne E-Mail muss abgelehnt werden")
	}
}

func TestBootstrapAndInstanceAdmin(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, true)
	boss, _ := s.Provision(ctx, users.Identity{IDP: "g", Subject: "b", Email: "boss@x.de"})
	anna, _ := s.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "anna@x.de"})
	if !boss.InstanceAdmin || anna.InstanceAdmin {
		t.Fatal("Bootstrap-Admin falsch erkannt")
	}
	if err := s.SetInstanceAdmin(ctx, boss.ID, false); !errors.Is(err, users.ErrBootstrap) {
		t.Errorf("Bootstrap-Admin änderbar: %v", err)
	}
	if err := s.SetInstanceAdmin(ctx, anna.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get(ctx, anna.ID); !a.InstanceAdmin {
		t.Error("Admin-Flag nicht gesetzt")
	}
	if err := s.SetInstanceAdmin(ctx, anna.ID, false); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get(ctx, anna.ID); a.InstanceAdmin {
		t.Error("Admin-Flag nicht entfernt")
	}
}

func TestDisableAndEnable(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, true)
	id := users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"}
	u, _ := s.Provision(ctx, id)
	if err := s.SetActive(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Provision(ctx, id); !errors.Is(err, users.ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetActive(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestGroups(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, true)
	u, _ := s.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"})
	g, err := s.CreateGroup(ctx, " Support ")
	if err != nil || g.Name != "Support" {
		t.Fatalf("%+v %v", g, err)
	}
	for _, dup := range []string{"Support", "support", "SUPPORT"} {
		if _, err := s.CreateGroup(ctx, dup); !errors.Is(err, users.ErrGroupExists) {
			t.Errorf("Duplikat %q: %v", dup, err)
		}
	}
	if got, err := s.GroupByName(ctx, "support"); err != nil || got.ID != g.ID {
		t.Errorf("GroupByName: %v", err)
	}
	if err := s.AddGroupMember(ctx, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroupMember(ctx, g.ID, u.ID); err != nil {
		t.Fatalf("zweites Hinzufügen muss idempotent sein: %v", err)
	}
	if m, _ := s.GroupMembers(ctx, g.ID); len(m) != 1 || m[0] != "a@x.de" {
		t.Errorf("Mitglieder = %v", m)
	}
	if err := s.RenameGroup(ctx, g.ID, "Kundendienst"); err != nil {
		t.Fatal(err)
	}
}

func TestIdPGroupsAreReadOnly(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	s := users.New(pool, users.Options{PersonalEnabled: true})
	var gid string
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, source, idp, external_id) VALUES ('Entra-Sales','idp','entra','abc') RETURNING id`).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	g, err := s.GroupByName(ctx, "entra-sales")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"})
	for name, err := range map[string]error{
		"add":    s.AddGroupMember(ctx, g.ID, u.ID),
		"remove": s.RemoveGroupMember(ctx, g.ID, u.ID),
		"rename": s.RenameGroup(ctx, g.ID, "x"),
		"delete": s.DeleteGroup(ctx, g.ID),
	} {
		if !errors.Is(err, users.ErrGroupReadOnly) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
