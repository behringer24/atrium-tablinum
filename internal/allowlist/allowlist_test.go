package allowlist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/allowlist"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
)

func TestNormalize(t *testing.T) {
	good := []struct{ kind, in, want string }{
		{"domain", " Example.COM ", "example.com"},
		{"domain", "mail.example.co.uk", "mail.example.co.uk"},
		{"email", "Anna@Example.com", "anna@example.com"},
		{"tenant", "72F988BF-86F1-41AF-91AB-2D7CD011DB47", "72f988bf-86f1-41af-91ab-2d7cd011db47"},
	}
	for _, c := range good {
		if got, err := allowlist.Normalize(c.kind, c.in); err != nil || got != c.want {
			t.Errorf("%s %q: %q %v", c.kind, c.in, got, err)
		}
	}
	bad := []struct{ kind, in string }{
		{"domain", "@example.com"}, {"domain", "*.example.com"}, {"domain", "example"}, {"domain", "exa mple.com"},
		{"domain", "example.com/evil"}, {"domain", ""},
		{"email", "example.com"}, {"email", "a@b@c.de"}, {"email", "Anna <a@x.de>"}, {"email", "a@x.de, b@x.de"},
		{"tenant", "contoso.onmicrosoft.com"}, {"tenant", "nope"},
		{"role", "x"},
	}
	for _, c := range bad {
		if _, err := allowlist.Normalize(c.kind, c.in); !errors.Is(err, allowlist.ErrInvalid) {
			t.Errorf("%s %q sollte ungültig sein: %v", c.kind, c.in, err)
		}
	}
}

func TestStoreAddListRemoveAndProviderScope(t *testing.T) {
	ctx := context.Background()
	s := allowlist.New(dbtest.NewPool(t))

	if added, err := s.Add(ctx, "domain", "Partner.de", "", uuid.Nil); err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	if added, err := s.Add(ctx, "domain", "partner.de", "", uuid.Nil); err != nil || added {
		t.Errorf("doppelt: %v %v", added, err)
	}
	if _, err := s.Add(ctx, "email", "gast@gmail.com", "google", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, "tenant", "72f988bf-86f1-41af-91ab-2d7cd011db47", "", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, "domain", "nur-keycloak.de", "kc", uuid.Nil); err != nil {
		t.Fatal(err)
	}

	google, _ := s.Additions(ctx, "google")
	if len(google.Domains) != 1 || google.Domains[0] != "partner.de" || len(google.Emails) != 1 || len(google.Tenants) != 1 {
		t.Errorf("google: %+v", google)
	}
	kc, _ := s.Additions(ctx, "kc")
	if len(kc.Domains) != 2 || len(kc.Emails) != 0 {
		t.Errorf("kc: %+v", kc)
	}
	if list, err := s.List(ctx); err != nil || len(list) != 4 {
		t.Errorf("List: %d %v", len(list), err)
	}

	if err := s.Remove(ctx, "domain", "nur-keycloak.de", ""); !errors.Is(err, allowlist.ErrNotFound) {
		t.Errorf("Entfernen mit falschem Provider: %v", err)
	}
	if err := s.Remove(ctx, "domain", "nur-keycloak.de", "kc"); err != nil {
		t.Fatal(err)
	}
	if kc, _ = s.Additions(ctx, "kc"); len(kc.Domains) != 1 {
		t.Errorf("kc nach dem Entfernen: %+v", kc)
	}
}
