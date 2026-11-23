package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// insertService adds a service account directly: nothing in this edition creates one.
func insertService(t *testing.T, pool *pgxpool.Pool, email, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, idp, idp_subject, kind) VALUES ($1, 'service', $2, 'service') RETURNING id`, email, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestServiceAccountIsNeverAnInstanceAdmin(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	s := users.New(pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true})

	// Even an address from bootstrap_admins gives a service account no admin right.
	sameAddress := insertService(t, pool, "boss@x.de", "boss-bot")
	u, err := s.Get(ctx, sameAddress)
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsService() || u.Kind != users.KindService || u.InstanceAdmin || u.ConfigAdmin {
		t.Errorf("service account with a bootstrap address: %+v", u)
	}

	other := insertService(t, pool, "docs-sync@service.invalid", "docs-sync")
	if err := s.SetInstanceAdmin(ctx, other, true); !errors.Is(err, users.ErrServiceAccount) {
		t.Errorf("granting the admin right: %v", err)
	}
	// The database refuses it as well, whoever tries.
	if _, err := pool.Exec(ctx, `UPDATE users SET instance_admin = true WHERE id = $1`, other); err == nil {
		t.Error("the database accepted an admin service account")
	}
	// Taking it away from an account that does not have it is no error.
	if err := s.SetInstanceAdmin(ctx, other, false); err != nil {
		t.Errorf("revoking: %v", err)
	}
}

func TestServiceAccountsDoNotCountAgainstTheUserLimit(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	s := users.New(pool, users.Options{PersonalEnabled: true, MaxUsers: func() int { return 2 }})
	svc1 := insertService(t, pool, "one@service.invalid", "one")
	insertService(t, pool, "two@service.invalid", "two")
	insertService(t, pool, "three@service.invalid", "three")

	if n, err := s.CountActive(ctx); err != nil || n != 0 {
		t.Fatalf("CountActive = %d, %v; service accounts must not count", n, err)
	}
	for _, name := range []string{"anna", "ben"} {
		if _, err := s.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: name, Email: name + "@x.de"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := s.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "carla", Email: "carla@x.de"}); !errors.Is(err, users.ErrUserLimit) {
		t.Errorf("third person: %v, expected the limit", err)
	}
	if n, _ := s.CountActive(ctx); n != 2 {
		t.Errorf("CountActive = %d, expected the two people", n)
	}

	// Locking and unlocking a service account needs no room, although the limit is reached.
	if err := s.SetActive(ctx, svc1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(ctx, svc1, true); err != nil {
		t.Errorf("reactivating a service account at the limit: %v", err)
	}
	u, err := s.Get(ctx, svc1)
	if err != nil || !u.Active {
		t.Errorf("after reactivation: %+v %v", u, err)
	}
}

// A person is a person: the kind column does not change what a login does.
func TestPersonKindIsTheDefault(t *testing.T) {
	ctx := context.Background()
	s := users.New(dbtest.NewPool(t), users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true})
	boss, err := s.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "b", Email: "boss@x.de"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, boss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsService() || got.Kind != users.KindPerson || !got.InstanceAdmin || !got.ConfigAdmin {
		t.Errorf("person: %+v", got)
	}
}
