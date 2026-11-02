package idp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/behringer24/atrium-tablinum/internal/config"
)

const (
	googleIssuer = "https://accounts.google.com"
	entraBase    = "https://login.microsoftonline.com"
)

type oidcProvider struct {
	cfg config.Provider

	mu       sync.Mutex
	endpoint oauth2.Endpoint
	verifier *oidc.IDTokenVerifier
	multiTID bool // Entra with several tenants: issuer differs per tenant and is checked by hand
}

func newOIDC(cfg config.Provider) *oidcProvider { return &oidcProvider{cfg: cfg} }

func (p *oidcProvider) ID() string { return p.cfg.ID }

func (p *oidcProvider) DisplayName() string {
	switch p.cfg.Type {
	case "google":
		return "Google"
	case "entra":
		return "Microsoft"
	}
	return p.cfg.ID
}

// discover resolves the IdP endpoints once; failures are retried on the next call.
func (p *oidcProvider) discover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verifier != nil {
		return nil
	}
	ctx = oidc.ClientContext(ctx, httpClient)

	issuer := p.cfg.Issuer
	oc := &oidc.Config{ClientID: p.cfg.ClientID}
	switch {
	case issuer != "": // oidc, or an override for google/entra (used in tests)
	case p.cfg.Type == "google":
		issuer = googleIssuer
	case p.cfg.Type == "entra":
		if len(p.cfg.AllowedTenants) == 1 {
			issuer = fmt.Sprintf("%s/%s/v2.0", entraBase, p.cfg.AllowedTenants[0])
		} else {
			// The "organizations" document advertises a {tenantid} issuer template.
			issuer = entraBase + "/organizations/v2.0"
			p.multiTID = true
			oc.SkipIssuerCheck = true
			ctx = oidc.InsecureIssuerURLContext(ctx, entraBase+"/{tenantid}/v2.0")
		}
	default:
		return errors.New("issuer fehlt")
	}
	prov, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("oidc-discovery für %s: %w", p.cfg.ID, err)
	}
	p.endpoint = prov.Endpoint()
	p.verifier = prov.Verifier(oc)
	return nil
}

func (p *oidcProvider) oauthConfig(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, Endpoint: p.endpoint, RedirectURL: redirect,
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

func (p *oidcProvider) AuthURL(ctx context.Context, r AuthRequest) (string, error) {
	if err := p.discover(ctx); err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", r.Nonce),
		oauth2.S256ChallengeOption(r.Verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	}
	// Mit freigegebenen Einzeladressen (evtl. Gmail ohne hd) wäre der Hinweis hinderlich.
	if p.cfg.Type == "google" && len(p.cfg.AllowedDomains) == 1 && len(p.cfg.AllowedEmails) == 0 {
		opts = append(opts, oauth2.SetAuthURLParam("hd", p.cfg.AllowedDomains[0])) // hint only
	}
	return p.oauthConfig(r.RedirectURL).AuthCodeURL(r.State, opts...), nil
}

func (p *oidcProvider) Exchange(ctx context.Context, code string, r AuthRequest) (Identity, error) {
	if err := p.discover(ctx); err != nil {
		return Identity{}, err
	}
	ctx = oidc.ClientContext(ctx, httpClient)
	tok, err := p.oauthConfig(r.RedirectURL).Exchange(ctx, code, oauth2.VerifierOption(r.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("code-austausch beim idp: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return Identity{}, errors.New("idp lieferte kein id_token")
	}
	idt, err := p.verifier.Verify(ctx, rawID) // signature, audience, expiry (+ issuer unless multi-tenant)
	if err != nil {
		return Identity{}, fmt.Errorf("id_token ungültig: %w", err)
	}
	var c claims
	if err := idt.Claims(&c); err != nil {
		return Identity{}, err
	}
	if c.Nonce == "" || c.Nonce != r.Nonce {
		return Identity{}, errors.New("nonce stimmt nicht überein")
	}
	if p.multiTID && idt.Issuer != fmt.Sprintf("%s/%s/v2.0", entraBase, c.TenantID) {
		return Identity{}, reject("issuer %q passt nicht zum tenant", idt.Issuer)
	}
	if p.cfg.Type == "entra" && strings.TrimSpace(c.TenantID) == "" {
		return Identity{}, reject("token ohne tid")
	}
	return c.check(p.cfg)
}
