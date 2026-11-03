package idp

import (
	"context"
	"errors"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/config"
)

type fakeAdditions struct {
	e   Entries
	err error
}

func (f fakeAdditions) Additions(context.Context, string) (Entries, error) { return f.e, f.err }

func TestEffectiveAddsPerProviderType(t *testing.T) {
	ctx := context.Background()
	extra := fakeAdditions{e: Entries{Domains: []string{"partner.de"}, Emails: []string{"gast@gmail.com"}, Tenants: []string{"t-2"}}}

	g := effective(ctx, config.Provider{ID: "g", Type: "google", AllowedDomains: []string{"example.com"}}, extra)
	if len(g.AllowedDomains) != 2 || len(g.AllowedEmails) != 1 || len(g.AllowedTenants) != 0 {
		t.Errorf("google: %+v", g)
	}
	// Entra: only tenants grow; a domain list there would restrict instead of approve
	en := effective(ctx, config.Provider{ID: "e", Type: "entra", AllowedTenants: []string{"t-1"}}, extra)
	if len(en.AllowedTenants) != 2 || len(en.AllowedDomains) != 0 || len(en.AllowedEmails) != 0 {
		t.Errorf("entra: %+v", en)
	}
	// the configuration itself is not modified
	base := config.Provider{ID: "g", Type: "google", AllowedDomains: make([]string, 1, 5)}
	_ = effective(ctx, base, extra)
	if len(base.AllowedDomains) != 1 || base.AllowedDomains[:2][1] != "" {
		t.Errorf("Konfiguration verändert: %+v", base)
	}
}

func TestEffectiveFailsClosed(t *testing.T) {
	cfg := config.Provider{ID: "g", Type: "google", AllowedDomains: []string{"example.com"}}
	got := effective(context.Background(), cfg, fakeAdditions{err: errors.New("db weg")})
	if len(got.AllowedDomains) != 1 {
		t.Errorf("bei Lesefehler gilt nur die Konfiguration: %+v", got)
	}
	if got := effective(context.Background(), cfg, nil); len(got.AllowedDomains) != 1 {
		t.Errorf("ohne Zusatzliste: %+v", got)
	}
}

func TestRuntimeAdditionsPassTheClaimsPolicy(t *testing.T) {
	ctx := context.Background()
	cfg := config.Provider{ID: "o", Type: "oidc", AllowedDomains: []string{"example.com"}}
	c := claims{Subject: "s", Email: "bob@partner.de", EmailVerified: true}
	if _, err := c.check(effective(ctx, cfg, nil)); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("ohne Zusatz: %v", err)
	}
	if _, err := c.check(effective(ctx, cfg, fakeAdditions{e: Entries{Domains: []string{"partner.de"}}})); err != nil {
		t.Errorf("mit Domain-Zusatz: %v", err)
	}
	// a single address does not open the whole domain
	one := fakeAdditions{e: Entries{Emails: []string{"bob@partner.de"}}}
	if _, err := c.check(effective(ctx, cfg, one)); err != nil {
		t.Errorf("mit Adress-Zusatz: %v", err)
	}
	c.Email = "eve@partner.de"
	if _, err := c.check(effective(ctx, cfg, one)); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("andere Adresse derselben Domain: %v", err)
	}
	// Entra: additional tenant
	en := config.Provider{ID: "e", Type: "entra", AllowedTenants: []string{"t-1"}}
	ec := claims{Subject: "s", Email: "bob@partner.de", TenantID: "t-2"}
	if _, err := ec.check(effective(ctx, en, nil)); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Entra ohne Zusatz: %v", err)
	}
	if _, err := ec.check(effective(ctx, en, fakeAdditions{e: Entries{Tenants: []string{"T-2"}}})); err != nil {
		t.Errorf("Entra mit Tenant-Zusatz: %v", err)
	}
}

func TestMultiTenantFollowsEffectiveTenants(t *testing.T) {
	p := newOIDC(config.Provider{ID: "e", Type: "entra", AllowedTenants: []string{"t-1"}})
	if p.multiTenant(config.Provider{AllowedTenants: []string{"t-1"}}) {
		t.Error("ein Tenant: Discovery des Tenants")
	}
	if !p.multiTenant(config.Provider{AllowedTenants: []string{"t-1", "t-2"}}) {
		t.Error("ein zur Laufzeit hinzugefügter Tenant braucht das organizations-Dokument")
	}
	if newOIDC(config.Provider{Type: "google"}).multiTenant(config.Provider{AllowedTenants: []string{"a", "b"}}) {
		t.Error("nur Entra")
	}
	if newOIDC(config.Provider{Type: "entra", Issuer: "https://idp.test"}).multiTenant(config.Provider{}) {
		t.Error("Issuer-Override (Tests) bleibt unberührt")
	}
}
