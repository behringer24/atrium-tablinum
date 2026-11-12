package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// The dev token has to work for an account that is also a bootstrap admin (the usual local setup),
// and it must not let any other token in.
func TestDevVerifierAcceptsBootstrapAdmin(t *testing.T) {
	pool := dbtest.NewPool(t)
	store := users.New(pool, users.Options{BootstrapAdmins: []string{"dev@example.test"}, PersonalEnabled: true})
	verify := DevVerifier(store, "dev-token-0123456789", "dev@example.test")

	if _, err := verify(context.Background(), "wrong-token-0123456789", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	ti, err := verify(context.Background(), "dev-token-0123456789", nil)
	if err != nil {
		t.Fatalf("dev token: %v", err)
	}
	id, err := uuid.Parse(ti.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !u.InstanceAdmin {
		t.Error("the dev account in bootstrap_admins should be an instance admin")
	}
}
