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
	u, err := e.users.Provision(e.ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: email, Email: email})
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

// Support staff member does not see "Geschäftsführung" (concept 17.3).
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
		t.Error("Management visible")
	}
	if !r.Can(support.ID, authz.Author) || r.Can(support.ID, authz.Curator) {
		t.Errorf("support-role = %v", r.Role(support.ID))
	}
}

func TestGroupMembershipGrantsAndRevokes(t *testing.T) {
	e := setup(t, time.Minute) // long TTL: invalidation must take effect
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
		t.Fatal("access without group membership")
	}
	if err := e.users.AddGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("group member without access")
	}
	if err := e.users.RemoveGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("access remained after removal from the group")
	}
	// Deleting a group also removes the space membership
	if err := e.users.AddGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.users.DeleteGroup(e.ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("access remained after deleting the group")
	}
	if m, _ := e.spaces.Members(e.ctx, sp.ID); len(m) != 0 {
		t.Errorf("orphaned memberships: %v", m)
	}
}

func TestDefaultRoleReaderIsReadOnly(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	sp := e.team(t, "firma", authz.Reader)
	r := e.roles(t, anna)
	if !r.Can(sp.ID, authz.Reader) || r.Can(sp.ID, authz.Author) {
		t.Errorf("role = %v", r.Role(sp.ID))
	}
}

func TestPersonalSpacesArePrivateEvenForAdmins(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	boss := e.user(t, "boss@x.de") // bootstrap admin, different capitalization
	if !boss.InstanceAdmin {
		t.Fatal("bootstrap admin not detected")
	}
	if anna.PersonalSpaceID == nil || boss.PersonalSpaceID == nil {
		t.Fatal("personal space missing")
	}
	ar, br := e.roles(t, anna), e.roles(t, boss)
	if !ar.Can(*anna.PersonalSpaceID, authz.Admin) {
		t.Error("owner without access to own space")
	}
	if br.Can(*anna.PersonalSpaceID, authz.Reader) {
		t.Error("instance admin reads foreign personal space")
	}
	if ar.Can(*boss.PersonalSpaceID, authz.Reader) {
		t.Error("user reads foreign personal space")
	}
	team := e.team(t, "geheim", authz.None)
	if !br.Can(team.ID, authz.Admin) && !e.roles(t, boss).Can(team.ID, authz.Admin) {
		t.Error("instance admin without access to team space")
	}
}

func TestArchivedSpaceHiddenExceptForInstanceAdmin(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	boss := e.user(t, "boss@x.de")
	sp := e.team(t, "alt", authz.Reader)
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Fatal("expected visible before")
	}
	if err := e.spaces.SetArchived(e.ctx, sp.ID, true); err != nil {
		t.Fatal(err)
	}
	if e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Error("archived space visible")
	}
	if !e.roles(t, boss).Can(sp.ID, authz.Admin) {
		t.Error("instance admin does not see archived space")
	}
	if err := e.spaces.SetArchived(e.ctx, sp.ID, false); err != nil {
		t.Fatal(err)
	}
	if !e.roles(t, anna).Can(sp.ID, authz.Reader) {
		t.Error("invisible after restore")
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
		t.Errorf("roles of a disabled user: %v", r)
	}
	if _, err := e.users.Provision(e.ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "anna@x.de", Email: "anna@x.de"}); err != users.ErrDisabled {
		t.Errorf("login of disabled user: %v", err)
	}
}

func TestCacheExpiryPicksUpDirectChanges(t *testing.T) {
	// Changes without the OnChange hook (e.g. made by another instance) take effect after the TTL.
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	us, ss := users.New(pool, users.Options{PersonalEnabled: true}), spaces.New(pool)
	ch := authz.NewChecker(pool, 50*time.Millisecond)
	// A fake clock: a slow database must not let the TTL run out between the steps.
	now := time.Now()
	ch.SetClock(func() time.Time { return now })
	anna, _ := us.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "a", Email: "a@x.de"})
	sp, _ := ss.CreateTeam(ctx, spaces.CreateParams{Slug: "t1", Name: "T"})

	r, _ := ch.EffectiveRoles(ctx, anna)
	if r.Can(sp.ID, authz.Reader) {
		t.Fatal("unexpected access")
	}
	if err := ss.SetMember(ctx, sp.ID, spaces.PrincipalUser, anna.ID, authz.Reader, anna.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = ch.EffectiveRoles(ctx, anna); r.Can(sp.ID, authz.Reader) {
		t.Fatal("cache was not used")
	}
	now = now.Add(80 * time.Millisecond)
	if r, _ = ch.EffectiveRoles(ctx, anna); !r.Can(sp.ID, authz.Reader) {
		t.Fatal("change not visible after TTL")
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
		t.Fatalf("spaces = %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected space %v", id)
		}
	}
}
