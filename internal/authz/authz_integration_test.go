package authz_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

type env struct {
	ctx     context.Context
	users   *users.Store
	spaces  *spaces.Store
	checker *authz.Checker
}

func setup(t *testing.T, ttl time.Duration) env {
	t.Helper()
	pool := dbtest.NewPool(t)
	e := env{
		ctx:     context.Background(),
		users:   users.New(pool, users.Options{BootstrapAdmins: []string{"Boss@x.de"}, PersonalEnabled: true}),
		spaces:  spaces.New(pool),
		checker: authz.NewChecker(pool, ttl),
	}
	e.users.OnChange = e.checker.InvalidateAll
	e.spaces.OnChange = e.checker.InvalidateAll
	return e
}

func (e env) user(t *testing.T, email string) users.User {
	t.Helper()
	u, err := e.users.Provision(e.ctx, users.Identity{IDP: "g", Subject: email, Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e env) team(t *testing.T, slug string, def authz.Role) spaces.Space {
	t.Helper()
	sp, err := e.spaces.CreateTeam(e.ctx, spaces.CreateParams{Slug: slug, Name: slug, DefaultRole: def})
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func (e env) roles(t *testing.T, u users.User) authz.Roles {
	t.Helper()
	r, err := e.checker.EffectiveRoles(e.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Support-Mitarbeiter sieht "Geschäftsführung" nicht (Konzept 17.3).
func TestUnrelatedSpaceInvisible(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	gf := e.team(t, "gf", authz.None)
	support := e.team(t, "support", authz.None)
	if err := e.spaces.SetMember(e.ctx, support.ID, spaces.PrincipalUser, anna.ID, authz.Author, anna.ID); err != nil {
		t.Fatal(err)
	}
	r := e.roles(t, anna)
	if r.Can(gf.ID, authz.Reader) {
		t.Error("Geschäftsführung sichtbar")
	}
	if !r.Can(support.ID, authz.Author) || r.Can(support.ID, authz.Curator) {
		t.Errorf("support-Rolle = %v", r.Role(support.ID))
	}
}

func TestGroupMembershipGrantsAndRevokes(t *testing.T) {
	e := setup(t, time.Minute) // lange TTL: Invalidierung muss greifen
	anna := e.user(t, "anna@x.de")
	sp := e.team(t, "vertrieb", authz.None)
	g, err := e.users.CreateGroup(e.ctx, "Vertrieb")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.spaces.SetMember(e.ctx, sp.ID, spaces.PrincipalGroup, g.ID, authz.Reader, anna.ID); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("Zugriff ohne Gruppenmitgliedschaft")
	}
	if err := e.users.AddGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("Gruppenmitglied ohne Zugriff")
	}
	if err := e.users.RemoveGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("Zugriff blieb nach Entfernen aus der Gruppe")
	}
	// Gruppe löschen entfernt auch die Bereichs-Mitgliedschaft
	if err := e.users.AddGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.users.DeleteGroup(e.ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("Zugriff blieb nach Löschen der Gruppe")
	}
	if m, _ := e.spaces.Members(e.ctx, sp.ID); len(m) != 0 {
		t.Errorf("verwaiste Mitgliedschaften: %v", m)
	}
}

func TestDefaultRoleReaderIsReadOnly(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	sp := e.team(t, "firma", authz.Reader)
	r := e.roles(t, anna)
	if !r.Can(sp.ID, authz.Reader) || r.Can(sp.ID, authz.Author) {
		t.Errorf("Rolle = %v", r.Role(sp.ID))
	}
}

func TestPersonalSpacesArePrivateEvenForAdmins(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	boss := e.user(t, "boss@x.de") // bootstrap admin, andere Schreibweise
	if !boss.InstanceAdmin {
		t.Fatal("Bootstrap-Admin nicht erkannt")
	}
	if anna.PersonalSpaceID == nil || boss.PersonalSpaceID == nil {
		t.Fatal("persönlicher Bereich fehlt")
	}
	ar, br := e.roles(t, anna), e.roles(t, boss)
	if !ar.Can(*anna.PersonalSpaceID, authz.Admin) {
		t.Error("Besitzerin ohne Zugriff auf eigenen Bereich")
	}
	if br.Can(*anna.PersonalSpaceID, authz.Reader) {
		t.Error("Instanz-Admin liest fremden persönlichen Bereich")
	}
	if ar.Can(*boss.PersonalSpaceID, authz.Reader) {
		t.Error("Nutzerin liest fremden persönlichen Bereich")
	}
	team := e.team(t, "geheim", authz.None)
	if !br.Can(team.ID, authz.Admin) && !e.roles(t, boss).Can(team.ID, authz.Admin) {
		t.Error("Instanz-Admin ohne Zugriff auf Team-Bereich")
	}
}

func TestArchivedSpaceHiddenExceptForInstanceAdmin(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	boss := e.user(t, "boss@x.de")
	sp := e.team(t, "alt", authz.Reader)
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("vorher sichtbar erwartet")
	}
	if err := e.spaces.SetArchived(e.ctx, sp.ID, true); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Error("archivierter Bereich sichtbar")
	}
	if !e.roles(t, boss).Can(sp.ID, authz.Admin) {
		t.Error("Instanz-Admin sieht archivierten Bereich nicht")
	}
	if err := e.spaces.SetArchived(e.ctx, sp.ID, false); err != nil {
		t.Fatal(err)
	}
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Error("nach Wiederherstellen unsichtbar")
	}
}

func TestDisabledUserHasNoRoles(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	sp := e.team(t, "firma", authz.Reader)
	if err := e.users.SetActive(e.ctx, anna.ID, false); err != nil {
		t.Fatal(err)
	}
	anna, _ = e.users.Get(e.ctx, anna.ID)
	if r := e.roles(t, anna); len(r) != 0 || r.Can(sp.ID, authz.Reader) {
		t.Errorf("Rollen eines deaktivierten Nutzers: %v", r)
	}
	if _, err := e.users.Provision(e.ctx, users.Identity{IDP: "g", Subject: "anna@x.de", Email: "anna@x.de"}); err != users.ErrDisabled {
		t.Errorf("Login deaktivierter Nutzer: %v", err)
	}
}

func TestCacheExpiryPicksUpDirectChanges(t *testing.T) {
	// Änderungen ohne OnChange-Hook (z. B. durch eine andere Instanz) greifen nach der TTL.
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	us, ss := users.New(pool, users.Options{PersonalEnabled: true}), spaces.New(pool)
	ch := authz.NewChecker(pool, 50*time.Millisecond)
	anna, _ := us.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "a@x.de"})
	sp, _ := ss.CreateTeam(ctx, spaces.CreateParams{Slug: "t1", Name: "T"})

	r, _ := ch.EffectiveRoles(ctx, anna)
	if r.Can(sp.ID, authz.Reader) {
		t.Fatal("unerwarteter Zugriff")
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, anna.ID, authz.Reader, anna.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = ch.EffectiveRoles(ctx, anna); r.Can(sp.ID, authz.Reader) {
		t.Fatal("Cache wurde nicht verwendet")
	}
	time.Sleep(80 * time.Millisecond)
	if r, _ = ch.EffectiveRoles(ctx, anna); !r.Can(sp.ID, authz.Reader) {
		t.Fatal("Änderung nach TTL nicht sichtbar")
	}
}

func TestSpacesListing(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	a := e.team(t, "aaa", authz.Reader)
	b := e.team(t, "bbb", authz.None)
	_ = b
	got := e.roles(t, anna).Spaces(authz.Reader)
	want := map[uuid.UUID]bool{a.ID: true, *anna.PersonalSpaceID: true}
	if len(got) != len(want) {
		t.Fatalf("Bereiche = %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unerwarteter Bereich %v", id)
		}
	}
}
