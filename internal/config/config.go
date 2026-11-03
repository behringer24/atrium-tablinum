// Package config loads and validates config.yaml. Secrets are referenced as ${ENV_VAR}
// in the YAML and expanded from the environment before parsing.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/product"
)

type Config struct {
	Instance  Instance  `yaml:"instance"`
	Auth      Auth      `yaml:"auth"`
	Database  Database  `yaml:"database"`
	Embedding Embedding `yaml:"embedding"`
	Reranker  Reranker  `yaml:"reranker"`
	Search    Search    `yaml:"search"`
	Chunking  Chunking  `yaml:"chunking"`
	Spaces    Spaces    `yaml:"spaces"`
	Policy    Policy    `yaml:"policy"`
	Tools     Tools     `yaml:"tools"`
}

type Instance struct {
	ProductName string `yaml:"product_name"`
	// ShortName is the form users say in a chat. Empty = "Atrium" for the default product
	// name, otherwise the product name itself.
	ShortName        string `yaml:"short_name"`
	OrganizationName string `yaml:"organization_name"`
	// BaseURL is the public HTTPS address clients see. It is the same whether TLS is
	// terminated by the bundled nginx, an existing reverse proxy or a tunnel.
	BaseURL string `yaml:"base_url"`
	Listen  string `yaml:"listen"`
	DevMode bool   `yaml:"dev_mode"`
	// TrustedProxies lists CIDRs (or single IPs) whose X-Forwarded-For/-Proto/-Host
	// headers are honoured. Empty = the server is reached directly and ignores them.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// DevToken is the static bearer token accepted when dev_mode is on. Empty = the
	// server requires one (it is never generated or logged). DevUser is the account it maps to.
	DevToken string `yaml:"dev_token"`
	DevUser  string `yaml:"dev_user"`
}

type Auth struct {
	Providers       []Provider    `yaml:"providers"`
	BootstrapAdmins []string      `yaml:"bootstrap_admins"`
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl"`
	// SessionMaxAge is the absolute lifetime of a login per client; after it the user logs in
	// again even if the connection is used all the time. 0 = unlimited.
	SessionMaxAge time.Duration `yaml:"session_max_age"`
	// LinkAccountsByEmail lets a login through a second provider take over the account with
	// the same e-mail. Off by default, see users.Options.LinkByEmail.
	LinkAccountsByEmail bool `yaml:"link_accounts_by_email"`
	// AllowedRedirectURIs are the callback URLs dynamic clients may register. A trailing
	// "*" matches any suffix (e.g. https://chatgpt.com/connector/oauth/*).
	AllowedRedirectURIs []string `yaml:"allowed_redirect_uris"`
	// AllowLoopbackRedirects also accepts http://localhost, 127.0.0.1 and [::1] with any
	// port, which local clients such as Claude Code use (RFC 8252).
	AllowLoopbackRedirects bool `yaml:"allow_loopback_redirects"`
}

type Provider struct {
	ID             string   `yaml:"id"`
	Type           string   `yaml:"type"` // google | entra | oidc
	Issuer         string   `yaml:"issuer"`
	ClientID       string   `yaml:"client_id"`
	ClientSecret   string   `yaml:"client_secret"`
	AllowedDomains []string `yaml:"allowed_domains"`
	// AllowedEmails admits individual addresses in addition to the domains (google, oidc),
	// e.g. private Gmail accounts without a Workspace domain.
	AllowedEmails  []string `yaml:"allowed_emails"`
	AllowedTenants []string `yaml:"allowed_tenants"`
	// GroupsClaim: for entra a bool-like switch (any non-empty value enables it),
	// for oidc the claim name. Kept as string so both shapes of the example parse.
	GroupsClaim string `yaml:"groups_claim"`
	// AllowUnverifiedEmail accepts generic OIDC logins whose ID token lacks
	// email_verified=true. Only for IdPs that never send the claim.
	AllowUnverifiedEmail bool `yaml:"allow_unverified_email"`
}

type Database struct {
	URL string `yaml:"url"`
}

type Embedding struct {
	Backend    string `yaml:"backend"` // tei | openai | bedrock (see embed.Backends)
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
	APIKey     string `yaml:"api_key"`
	// QueryPrefix and DocumentPrefix are put in front of every text, for models that
	// need them (E5: "query: " / "passage: ").
	QueryPrefix    string `yaml:"query_prefix"`
	DocumentPrefix string `yaml:"document_prefix"`
	// Timeout bounds one request to the embedding backend.
	Timeout time.Duration `yaml:"timeout"`
	// Options are the settings only one backend knows; see docs/embedding.md.
	Options map[string]string `yaml:"options"`
}

// Settings converts the config section for the embed package.
func (e Embedding) Settings() embed.Settings {
	return embed.Settings{
		Backend: e.Backend, URL: e.URL, Model: e.Model, Dimensions: e.Dimensions, APIKey: e.APIKey,
		QueryPrefix: e.QueryPrefix, DocumentPrefix: e.DocumentPrefix, Timeout: e.Timeout, Options: e.Options,
	}
}

type Reranker struct {
	Enabled    bool   `yaml:"enabled"`
	Backend    string `yaml:"backend"`
	URL        string `yaml:"url"`
	Candidates int    `yaml:"candidates"`
}

type Search struct {
	FTSLanguages        []string `yaml:"fts_languages"`
	CandidatesPerMethod int      `yaml:"candidates_per_method"`
	RRFK                int      `yaml:"rrf_k"`
	DefaultLimit        int      `yaml:"default_limit"`
	MaxLimit            int      `yaml:"max_limit"`
	HNSWEfSearch        int      `yaml:"hnsw_ef_search"`
}

type Chunking struct {
	TargetTokens int `yaml:"target_tokens"`
	MaxTokens    int `yaml:"max_tokens"`
}

type Spaces struct {
	Personal         PersonalSpaces    `yaml:"personal"`
	HandoffTTLDays   int               `yaml:"handoff_ttl_days"`
	DefaultSpaceByGr map[string]string `yaml:"default_space_by_group"`
}

type PersonalSpaces struct {
	Enabled                       bool  `yaml:"enabled"`
	MaxDocuments                  int   `yaml:"max_documents"`
	MaxBytes                      int64 `yaml:"max_bytes"`
	RetentionDaysAfterOffboarding int   `yaml:"retention_days_after_offboarding"`
}

type Policy struct {
	DuplicateThreshold       float64 `yaml:"duplicate_threshold"`
	MaxDocumentBytes         int     `yaml:"max_document_bytes"`
	RateLimitWritesPerMinute int     `yaml:"rate_limit_writes_per_minute"`
	// RateLimitReadsPerMinute caps search, read and list calls per user (each search costs an
	// embedding); 0 = unlimited.
	RateLimitReadsPerMinute int `yaml:"rate_limit_reads_per_minute"`
}

type Tools struct {
	Descriptions map[string]string `yaml:"descriptions"`
}

// Defaults returns the configuration used for every field the YAML leaves out.
func Defaults() Config {
	return Config{
		Instance: Instance{
			ProductName: product.DefaultName,
			Listen:      ":8080",
			DevUser:     "dev@example.test",
		},
		Auth: Auth{
			AccessTokenTTL:         time.Hour,
			RefreshTokenTTL:        720 * time.Hour,
			SessionMaxAge:          2160 * time.Hour,
			AllowLoopbackRedirects: true,
		},
		Embedding: Embedding{Backend: "tei", Model: "BAAI/bge-m3", Dimensions: 1024, Timeout: 60 * time.Second},
		Reranker:  Reranker{Backend: "tei", Candidates: 30},
		Search: Search{
			FTSLanguages:        []string{"german", "english", "simple"},
			CandidatesPerMethod: 50,
			RRFK:                60,
			DefaultLimit:        5,
			MaxLimit:            20,
			HNSWEfSearch:        100,
		},
		Chunking: Chunking{TargetTokens: 500, MaxTokens: 800},
		Spaces: Spaces{
			Personal: PersonalSpaces{
				Enabled:                       true,
				MaxDocuments:                  500,
				MaxBytes:                      10 << 20,
				RetentionDaysAfterOffboarding: 30,
			},
			HandoffTTLDays: 14,
		},
		Policy: Policy{
			DuplicateThreshold:       0.90,
			MaxDocumentBytes:         200000,
			RateLimitWritesPerMinute: 20,
			RateLimitReadsPerMinute:  120,
		},
	}
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads path, expands ${VAR} references, applies defaults and validates.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(raw, os.LookupEnv)
}

// Parse is Load without the file system; lookup resolves ${VAR} references.
func Parse(raw []byte, lookup func(string) (string, bool)) (*Config, error) {
	var missing []string
	expanded := envRef.ReplaceAllStringFunc(string(raw), func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := lookup(name)
		if !ok || v == "" {
			missing = append(missing, name)
		}
		return v
	})
	// A ${VAR} inside a YAML comment must not fail the load, so only report
	// variables that survive in non-comment lines.
	missing = filterUncommented(string(raw), missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing environment variables: %s", strings.Join(dedupe(missing), ", "))
	}

	cfg := Defaults()
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Instance.ShortName == "" {
		cfg.Instance.ShortName = cfg.Instance.ProductName
		if cfg.Instance.ProductName == product.DefaultName {
			cfg.Instance.ShortName = product.DefaultShortName
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks cross-field rules and returns all problems at once.
func (c *Config) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if c.Instance.ProductName == "" {
		add("instance.product_name must not be empty")
	}
	if c.Instance.Listen == "" {
		add("instance.listen must not be empty")
	}
	if u, err := url.Parse(c.Instance.BaseURL); err != nil || u.Host == "" {
		add("instance.base_url must be an absolute URL")
	} else if u.Scheme != "https" && !(c.Instance.DevMode && isLoopback(u.Hostname())) {
		add("instance.base_url must use https (http only with dev_mode and localhost)")
	}
	for _, p := range c.Instance.TrustedProxies {
		if !validIPOrCIDR(p) {
			add("instance.trusted_proxies: %q is neither an IP nor a CIDR", p)
		}
	}
	if c.Instance.DevMode {
		// A static token without expiry is only acceptable where nobody else can reach it.
		if u, err := url.Parse(c.Instance.BaseURL); err == nil && !isLoopback(u.Hostname()) {
			add("instance.dev_mode is only allowed with a base_url on localhost")
		}
		if c.Instance.DevToken == "" {
			add("instance.dev_mode needs its own instance.dev_token (at least 16 characters); it is no longer generated and logged")
		}
	}
	if c.Instance.DevToken != "" && !c.Instance.DevMode {
		add("instance.dev_token is only allowed with dev_mode")
	}
	if c.Instance.DevToken != "" && len(c.Instance.DevToken) < 16 {
		add("instance.dev_token must be at least 16 characters")
	}
	if c.Database.URL == "" {
		add("database.url is missing")
	}

	if len(c.Auth.Providers) == 0 && !c.Instance.DevMode {
		add("auth.providers: at least one Identity Provider required (or dev_mode)")
	}
	seen := map[string]bool{}
	for i, p := range c.Auth.Providers {
		at := fmt.Sprintf("auth.providers[%d]", i)
		if p.ID == "" || seen[p.ID] {
			add("%s.id is missing or duplicated", at)
		}
		seen[p.ID] = true
		if p.ClientID == "" || p.ClientSecret == "" {
			add("%s: client_id and client_secret required", at)
		}
		switch p.Type {
		case "google":
			if len(p.AllowedDomains) == 0 && len(p.AllowedEmails) == 0 {
				add("%s: allowed_domains or allowed_emails required", at)
			}
		case "entra":
			if len(p.AllowedTenants) == 0 {
				add("%s: allowed_tenants required", at)
			}
			if len(p.AllowedEmails) > 0 {
				add("%s: allowed_emails is not supported for entra", at)
			}
		case "oidc":
			if p.Issuer == "" {
				add("%s: issuer required", at)
			}
			if len(p.AllowedDomains) == 0 && len(p.AllowedEmails) == 0 {
				add("%s: allowed_domains or allowed_emails required", at)
			}
		default:
			add("%s.type %q unknown (google | entra | oidc)", at, p.Type)
		}
	}
	for _, u := range c.Auth.AllowedRedirectURIs {
		base := strings.TrimSuffix(u, "*")
		if pu, err := url.Parse(base); err != nil || pu.Scheme != "https" || pu.Host == "" || pu.Fragment != "" {
			add("auth.allowed_redirect_uris: %q must be an https URL", u)
		}
	}
	if c.Auth.AccessTokenTTL <= 0 || c.Auth.RefreshTokenTTL <= 0 {
		add("auth: token TTLs must be positive")
	}

	if c.Embedding.Dimensions <= 0 || c.Embedding.Dimensions > 2000 {
		// pgvector indexes at most 2000 dimensions with HNSW.
		add("embedding.dimensions must be between 1 and 2000")
	} else if err := embed.Validate(c.Embedding.Settings()); err != nil {
		add("%s", err)
	}
	if c.Reranker.Enabled && c.Reranker.URL == "" {
		add("reranker.url is missing")
	}

	hasSimple := false
	for _, l := range c.Search.FTSLanguages {
		if l == "simple" {
			hasSimple = true
		}
	}
	if !hasSimple {
		add(`search.fts_languages must contain "simple"`)
	}
	if c.Search.DefaultLimit < 1 || c.Search.DefaultLimit > c.Search.MaxLimit {
		add("search: default_limit must be between 1 and max_limit")
	}
	if c.Search.CandidatesPerMethod < 1 || c.Search.RRFK < 1 {
		add("search: candidates_per_method and rrf_k must be positive")
	}
	if c.Chunking.TargetTokens < 1 || c.Chunking.MaxTokens < c.Chunking.TargetTokens {
		add("chunking: max_tokens must be >= target_tokens > 0")
	}
	if c.Policy.DuplicateThreshold <= 0 || c.Policy.DuplicateThreshold > 1 {
		add("policy.duplicate_threshold must be in (0, 1]")
	}
	if c.Policy.MaxDocumentBytes < 1 {
		add("policy.max_document_bytes must be positive")
	}
	if c.Spaces.HandoffTTLDays < 1 {
		add("spaces.handoff_ttl_days must be >= 1")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validIPOrCIDR(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func filterUncommented(raw string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	live := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		for _, m := range envRef.FindAllStringSubmatch(line, -1) {
			live[m[1]] = true
		}
	}
	var out []string
	for _, n := range names {
		if live[n] {
			out = append(out, n)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
