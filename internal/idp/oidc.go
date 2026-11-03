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

// variant is the discovery result for one way of talking to the IdP. Entra needs two: the
// issuer of a single tenant, or the "organizations" document with a per-tenant issuer.
type variant struct {
	endpoint oauth2.Endpoint
	verifier *oidc.IDTokenVerifier
}

type oidcProvider struct {
	cfg   config.Provider
	extra Additions // runtime allowlist entries, may be nil

	mu       sync.Mutex
	variants map[bool]*variant // key: multi-tenant (Entra with other than exactly one tenant)
}

func newOIDC(cfg config.Provider) *oidcProvider {
	return &oidcProvider{cfg: cfg, variants: map[bool]*variant{}}
}

// SetAdditions wires the runtime allowlist; call it before the provider serves logins.
func (p *oidcProvider) SetAdditions(a Additions) { p.extra = a }

// multiTenant tells whether the effective tenant list needs the multi-tenant document. A
// tenant an admin adds at runtime to a single-tenant setup switches it over.
func (p *oidcProvider) multiTenant(eff config.Provider) bool {
	return p.cfg.Type == "entra" && p.cfg.Issuer == "" && len(eff.AllowedTenants) != 1
}

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
func (p *oidcProvider) discover(ctx context.Context, eff config.Provider) (*variant, error) {
	multi := p.multiTenant(eff)
	p.mu.Lock()
	defer p.mu.Unlock()
	if v := p.variants[multi]; v != nil {
		return v, nil
	}
	ctx = oidc.ClientContext(ctx, httpClient)

	issuer := p.cfg.Issuer
	oc := &oidc.Config{ClientID: p.cfg.ClientID}
	switch {
	case issuer != "": // oidc, or an override for google/entra (used in tests)
	case p.cfg.Type == "google":
		issuer = googleIssuer
	case p.cfg.Type == "entra":
		if !multi {
			issuer = fmt.Sprintf("%s/%s/v2.0", entraBase, eff.AllowedTenants[0])
		} else {
			// The "organizations" document advertises a {tenantid} issuer template.
			issuer = entraBase + "/organizations/v2.0"
			oc.SkipIssuerCheck = true
			ctx = oidc.InsecureIssuerURLContext(ctx, entraBase+"/{tenantid}/v2.0")
		}
	default:
		return nil, errors.New("issuer fehlt")
	}
	prov, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc-discovery für %s: %w", p.cfg.ID, err)
	}
	v := &variant{endpoint: prov.Endpoint(), verifier: prov.Verifier(oc)}
	p.variants[multi] = v
	return v, nil
}

func (p *oidcProvider) oauthConfig(v *variant, redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, Endpoint: v.endpoint, RedirectURL: redirect,
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

func (p *oidcProvider) AuthURL(ctx context.Context, r AuthRequest) (string, error) {
	eff := effective(ctx, p.cfg, p.extra)
	v, err := p.discover(ctx, eff)
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", r.Nonce),
		oauth2.S256ChallengeOption(r.Verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	}
	// Mit freigegebenen Einzeladressen (evtl. Gmail ohne hd) wäre der Hinweis hinderlich.
	if p.cfg.Type == "google" && len(eff.AllowedDomains) == 1 && len(eff.AllowedEmails) == 0 {
		opts = append(opts, oauth2.SetAuthURLParam("hd", eff.AllowedDomains[0])) // hint only
	}
	return p.oauthConfig(v, r.RedirectURL).AuthCodeURL(r.State, opts...), nil
}

func (p *oidcProvider) Exchange(ctx context.Context, code string, r AuthRequest) (Identity, error) {
	eff := effective(ctx, p.cfg, p.extra)
	v, err := p.discover(ctx, eff)
	if err != nil {
		return Identity{}, err
	}
	ctx = oidc.ClientContext(ctx, httpClient)
	tok, err := p.oauthConfig(v, r.RedirectURL).Exchange(ctx, code, oauth2.VerifierOption(r.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("code-austausch beim idp: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return Identity{}, errors.New("idp lieferte kein id_token")
	}
	idt, err := v.verifier.Verify(ctx, rawID) // signature, audience, expiry (+ issuer unless multi-tenant)
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
	if p.multiTenant(eff) && idt.Issuer != fmt.Sprintf("%s/%s/v2.0", entraBase, c.TenantID) {
		return Identity{}, reject("issuer %q passt nicht zum tenant", idt.Issuer)
	}
	if p.cfg.Type == "entra" && strings.TrimSpace(c.TenantID) == "" {
		return Identity{}, reject("token ohne tid")
	}
	return c.check(eff)
}
