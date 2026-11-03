// Package idp logs users in through external identity providers (Google, Microsoft Entra
// ID, generic OIDC) and enforces the domain/tenant policy on the resulting ID token.
// The server is its own OAuth authorization server; providers are used for login only.
package idp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/config"
)

// ErrNotAllowed means the login was valid but policy rejects the account (wrong domain,
// tenant, unverified e-mail …). The wrapped text is for logs, not for end users.
var ErrNotAllowed = errors.New("konto nicht zugelassen")

// Identity is the verified result of a login.
type Identity struct {
	Subject     string
	Email       string
	DisplayName string
	Groups      []string // from the groups claim, if present (used from phase 2)
}

type AuthRequest struct {
	State       string
	Nonce       string
	Verifier    string // PKCE verifier toward the IdP; its S256 challenge is sent
	RedirectURL string
}

// Provider is one configured identity provider.
type Provider interface {
	ID() string
	// DisplayName is shown on the provider chooser page.
	DisplayName() string
	AuthURL(ctx context.Context, r AuthRequest) (string, error)
	// Exchange trades the authorization code for an ID token, verifies it and applies
	// the account policy.
	Exchange(ctx context.Context, code string, r AuthRequest) (Identity, error)
}

// New builds a provider from its config entry. Discovery with the IdP happens lazily on
// first use, so the server starts even if the IdP is briefly unreachable.
func New(cfg config.Provider) (Provider, error) {
	switch cfg.Type {
	case "google", "entra", "oidc":
		return newOIDC(cfg), nil
	}
	return nil, fmt.Errorf("unbekannter provider-typ %q", cfg.Type)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// claims are the ID token fields the policy looks at.
type claims struct {
	Subject           string   `json:"sub"`
	Nonce             string   `json:"nonce"`
	Email             string   `json:"email"`
	EmailVerified     boolish  `json:"email_verified"`
	Name              string   `json:"name"`
	HostedDomain      string   `json:"hd"`
	TenantID          string   `json:"tid"`
	PreferredUsername string   `json:"preferred_username"`
	UPN               string   `json:"upn"`
	Groups            []string `json:"groups"`
}

// boolish accepts true/false and "true"/"false" (some IdPs send strings).
type boolish bool

func (b *boolish) UnmarshalJSON(d []byte) error {
	s := strings.Trim(string(d), `"`)
	*b = s == "true"
	return nil
}

func (c claims) email() string {
	for _, cand := range []string{c.Email, c.UPN, c.PreferredUsername} {
		if cand = strings.ToLower(strings.TrimSpace(cand)); strings.Contains(cand, "@") {
			return cand
		}
	}
	return ""
}

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotAllowed, fmt.Sprintf(format, a...))
}

// check applies the per-type account policy of concept section 6.2.
func (c claims) check(cfg config.Provider) (Identity, error) {
	if c.Subject == "" {
		return Identity{}, reject("token ohne sub")
	}
	email := c.email()
	if email == "" {
		return Identity{}, reject("token ohne e-mail-adresse")
	}
	domain := email[strings.LastIndex(email, "@")+1:]

	// Einzeln freigegebene Adressen (z. B. private Gmail-Konten) umgehen die Domain-Prüfung,
	// nicht aber die Verifizierung der E-Mail-Adresse.
	byEmail := containsFold(cfg.AllowedEmails, email)

	switch cfg.Type {
	case "google":
		if !c.EmailVerified {
			return Identity{}, reject("e-mail nicht verifiziert")
		}
		// The hd parameter in the login URL is only a hint; the claim decides.
		if !byEmail && !containsFold(cfg.AllowedDomains, c.HostedDomain) {
			return Identity{}, reject("hd-claim %q nicht erlaubt", c.HostedDomain)
		}
	case "entra":
		// Entra has no email_verified claim; accounts are bound to their tenant instead.
		if !containsFold(cfg.AllowedTenants, c.TenantID) {
			return Identity{}, reject("tenant %q nicht erlaubt", c.TenantID)
		}
	case "oidc":
		if !bool(c.EmailVerified) && !cfg.AllowUnverifiedEmail {
			return Identity{}, reject("e-mail nicht verifiziert")
		}
		if !byEmail && len(cfg.AllowedDomains) == 0 {
			return Identity{}, reject("e-mail-adresse nicht freigegeben")
		}
	default:
		return Identity{}, reject("unbekannter provider-typ")
	}
	if !byEmail && len(cfg.AllowedDomains) > 0 && !containsFold(cfg.AllowedDomains, domain) {
		return Identity{}, reject("domain %q nicht erlaubt", domain)
	}

	name := strings.TrimSpace(c.Name)
	return Identity{Subject: c.Subject, Email: email, DisplayName: name, Groups: c.Groups}, nil
}

func containsFold(list []string, v string) bool {
	if v == "" {
		return false
	}
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), v) {
			return true
		}
	}
	return false
}

func decodeClaims(raw json.RawMessage) (claims, error) {
	var c claims
	return c, json.Unmarshal(raw, &c)
}
