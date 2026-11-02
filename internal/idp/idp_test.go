package idp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/behringer24/atrium-tablinum/internal/config"
	"github.com/behringer24/atrium-tablinum/internal/idp/idptest"
)

func TestClaimsPolicy(t *testing.T) {
	google := config.Provider{Type: "google", AllowedDomains: []string{"example.com"}}
	entra := config.Provider{Type: "entra", AllowedTenants: []string{"T-1"}, AllowedDomains: []string{"example.com"}}
	oidcCfg := config.Provider{Type: "oidc", AllowedDomains: []string{"example.com"}}
	googleMail := config.Provider{Type: "google", AllowedEmails: []string{"me@gmail.com"}}
	googleBoth := config.Provider{Type: "google", AllowedDomains: []string{"example.com"}, AllowedEmails: []string{"me@gmail.com"}}
	oidcMail := config.Provider{Type: "oidc", AllowedEmails: []string{"me@gmail.com"}}
	ok := claims{Subject: "s", Email: "Anna@Example.com", EmailVerified: true, HostedDomain: "example.com", TenantID: "t-1"}

	cases := []struct {
		name string
		cfg  config.Provider
		mod  func(*claims)
		want string // substring of the rejection, "" = accepted
	}{
		{"google ok", google, nil, ""},
		{"google unverified", google, func(c *claims) { c.EmailVerified = false }, "verifiziert"},
		{"google without hd (private account)", google, func(c *claims) { c.HostedDomain = "" }, "hd"},
		{"google wrong hd", google, func(c *claims) { c.HostedDomain = "evil.com" }, "hd"},
		{"google hd ok but email domain differs", google, func(c *claims) { c.Email = "a@evil.com" }, "domain"},
		{"google gmail listed by email", googleMail, func(c *claims) { c.Email = "Me@Gmail.com"; c.HostedDomain = "" }, ""},
		{"google gmail not listed", googleMail, func(c *claims) { c.Email = "other@gmail.com"; c.HostedDomain = "" }, "hd"},
		{"google listed email still needs verification", googleMail, func(c *claims) { c.Email = "me@gmail.com"; c.HostedDomain = ""; c.EmailVerified = false }, "verifiziert"},
		{"google emails only, domain account not listed", googleMail, nil, "hd"},
		{"google domain and emails combined, domain member", googleBoth, nil, ""},
		{"google domain and emails combined, listed gmail", googleBoth, func(c *claims) { c.Email = "me@gmail.com"; c.HostedDomain = "" }, ""},
		{"oidc listed email without domains", oidcMail, func(c *claims) { c.Email = "me@gmail.com" }, ""},
		{"oidc unlisted email without domains", oidcMail, nil, "freigegeben"},
		{"entra ok without verified flag", entra, func(c *claims) { c.EmailVerified = false }, ""},
		{"entra tenant case-insensitive", entra, func(c *claims) { c.TenantID = "T-1" }, ""},
		{"entra wrong tenant", entra, func(c *claims) { c.TenantID = "other" }, "tenant"},
		{"entra email from upn", entra, func(c *claims) { c.Email = ""; c.UPN = "bob@example.com" }, ""},
		{"entra email from preferred_username", entra, func(c *claims) { c.Email = ""; c.PreferredUsername = "bob@example.com" }, ""},
		{"entra wrong domain", entra, func(c *claims) { c.Email = "bob@evil.com" }, "domain"},
		{"oidc ok", oidcCfg, nil, ""},
		{"oidc unverified", oidcCfg, func(c *claims) { c.EmailVerified = false }, "verifiziert"},
		{"oidc unverified allowed by config", config.Provider{Type: "oidc", AllowedDomains: []string{"example.com"}, AllowUnverifiedEmail: true},
			func(c *claims) { c.EmailVerified = false }, ""},
		{"no sub", oidcCfg, func(c *claims) { c.Subject = "" }, "sub"},
		{"no email", oidcCfg, func(c *claims) { c.Email = "" }, "e-mail"},
		{"username without @ is no email", oidcCfg, func(c *claims) { c.Email = ""; c.PreferredUsername = "bob" }, "e-mail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ok
			if tc.mod != nil {
				tc.mod(&c)
			}
			id, err := c.check(tc.cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unerwartet abgelehnt: %v", err)
				}
				if id.Email != strings.ToLower(id.Email) {
					t.Errorf("E-Mail nicht normalisiert: %s", id.Email)
				}
				return
			}
			if !errors.Is(err, ErrNotAllowed) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, erwartet %q", err, tc.want)
			}
		})
	}
}

func TestEmailVerifiedAcceptsStrings(t *testing.T) {
	c, err := decodeClaims([]byte(`{"sub":"s","email":"a@x.de","email_verified":"true"}`))
	if err != nil || !c.EmailVerified {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = decodeClaims([]byte(`{"email_verified":false}`))
	if c.EmailVerified {
		t.Error("false wurde true")
	}
}

// Full code flow against the fake IdP: discovery, PKCE, nonce, signature, claims.
func TestOIDCProviderRoundTrip(t *testing.T) {
	fake := idptest.New(t)
	p, err := New(config.Provider{ID: "kc", Type: "oidc", Issuer: fake.URL, ClientID: idptest.ClientID,
		ClientSecret: idptest.ClientSecret, AllowedDomains: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := AuthRequest{State: "st", Nonce: "nonce-1", Verifier: oauth2.GenerateVerifier(), RedirectURL: "https://app.example/cb"}

	authURL, err := p.AuthURL(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") != "nonce-1" || q.Get("state") != "st" || q.Get("redirect_uri") != req.RedirectURL {
		t.Fatalf("Auth-URL: %v", q)
	}
	code := approve(t, authURL)

	id, err := p.Exchange(ctx, code, req)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "user-1" || id.Email != "anna@example.com" || id.DisplayName != "Anna" {
		t.Errorf("%+v", id)
	}

	// wrong nonce -> rejected
	code = approve(t, authURL)
	bad := req
	bad.Nonce = "other"
	if _, err := p.Exchange(ctx, code, bad); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Errorf("falsche Nonce: %v", err)
	}
	// wrong PKCE verifier -> IdP refuses the exchange
	code = approve(t, authURL)
	bad = req
	bad.Verifier = oauth2.GenerateVerifier()
	if _, err := p.Exchange(ctx, code, bad); err == nil {
		t.Error("falscher Verifier akzeptiert")
	}
	// policy applies: foreign domain
	fake.SetClaims(map[string]any{"email": "eve@evil.com"})
	code = approve(t, authURL)
	if _, err := p.Exchange(ctx, code, req); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("fremde Domain: %v", err)
	}
}

func TestGoogleTypeUsesIssuerOverrideAndHD(t *testing.T) {
	fake := idptest.New(t)
	p, _ := New(config.Provider{ID: "g", Type: "google", Issuer: fake.URL, ClientID: idptest.ClientID,
		ClientSecret: idptest.ClientSecret, AllowedDomains: []string{"example.com"}})
	req := AuthRequest{State: "s", Nonce: "n", Verifier: oauth2.GenerateVerifier(), RedirectURL: "https://app.example/cb"}
	authURL, err := p.AuthURL(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(authURL); u.Query().Get("hd") != "example.com" {
		t.Errorf("hd-Hinweis fehlt: %s", authURL)
	}
	fake.SetClaims(map[string]any{"hd": "example.com"})
	if _, err := p.Exchange(context.Background(), approve(t, authURL), req); err != nil {
		t.Errorf("Workspace-Konto: %v", err)
	}
	fake.SetClaims(nil) // kein hd-Claim: privates Konto
	if _, err := p.Exchange(context.Background(), approve(t, authURL), req); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("privates Konto: %v", err)
	}
}

func TestDiscoveryFailureIsRetried(t *testing.T) {
	p, _ := New(config.Provider{ID: "x", Type: "oidc", Issuer: "http://127.0.0.1:1", ClientID: "c", ClientSecret: "s"})
	if _, err := p.AuthURL(context.Background(), AuthRequest{}); err == nil {
		t.Fatal("Discovery gegen toten Port muss fehlschlagen")
	}
	if _, err := p.AuthURL(context.Background(), AuthRequest{}); err == nil {
		t.Fatal("zweiter Versuch muss ebenfalls fehlschlagen, nicht den Fehler cachen")
	}
}

func TestUnknownType(t *testing.T) {
	if _, err := New(config.Provider{Type: "saml"}); err == nil {
		t.Fatal("unbekannter Typ")
	}
}

// approve follows the fake IdP's authorization endpoint once and returns the code.
func approve(t *testing.T, authURL string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("keine Weiterleitung (Status %d)", resp.StatusCode)
	}
	return loc.Query().Get("code")
}
