package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

const minimal = `
instance:
  base_url: "https://knowledge.example.com"
auth:
  providers:
    - id: google
      type: google
      client_id: "id"
      client_secret: "${GOOGLE_SECRET}"
      allowed_domains: ["example.com"]
database:
  url: "${DATABASE_URL}"
embedding:
  url: "http://tei-embed:80"
`

func TestParseMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimal), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance.ProductName != "Atrium Tablinum" || cfg.Instance.ShortName != "Atrium" || cfg.Instance.Listen != ":8080" {
		t.Errorf("defaults missing: %+v", cfg.Instance)
	}
	if cfg.Auth.Providers[0].ClientSecret != "s" || cfg.Database.URL != "postgres://x" {
		t.Error("environment variables not expanded")
	}
	if cfg.Embedding.Dimensions != 1024 {
		t.Errorf("dimensions = %d", cfg.Embedding.Dimensions)
	}
}

func TestEmbeddingSectionFromYAML(t *testing.T) {
	raw := strings.Replace(minimal, "embedding:\n  url: \"http://tei-embed:80\"", `embedding:
  backend: openai
  model: "text-embedding-3-small"
  dimensions: 1536
  api_key: "k"
  query_prefix: "query: "
  document_prefix: "passage: "
  timeout: 15s
  options:
    send_dimensions: true
    batch_size: 8`, 1)
	cfg, err := Parse([]byte(raw), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Embedding
	if e.Backend != "openai" || e.Dimensions != 1536 || e.QueryPrefix != "query: " || e.DocumentPrefix != "passage: " ||
		e.Timeout != 15*time.Second || e.Options["send_dimensions"] != "true" || e.Options["batch_size"] != "8" {
		t.Errorf("embedding section: %+v", e)
	}
	if s := e.Settings(); s.Options["batch_size"] != "8" || s.Timeout != 15*time.Second || s.QueryPrefix != "query: " {
		t.Errorf("Settings: %+v", s)
	}
	// a mistyped option is rejected on load
	bad := strings.Replace(raw, "batch_size: 8", "batchsize: 8", 1)
	if _, err := Parse([]byte(bad), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"})); err == nil ||
		!strings.Contains(err.Error(), "batch_size") {
		t.Errorf("typo in options: %v", err)
	}
}

func TestShortName(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, product, short string }{
		"Standard":                       {"", "Atrium Tablinum", "Atrium"},
		"custom name without short name": {"  product_name: \"Knowledge-Hub\"\n", "Knowledge-Hub", "Knowledge-Hub"},
		"custom short name":              {"  product_name: \"Knowledge-Hub Pro\"\n  short_name: \"Hub\"\n", "Knowledge-Hub Pro", "Hub"},
	} {
		raw := strings.Replace(minimal, "instance:\n", "instance:\n"+tc.yaml, 1)
		cfg, err := Parse([]byte(raw), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Instance.ProductName != tc.product || cfg.Instance.ShortName != tc.short {
			t.Errorf("%s: %q / %q", name, cfg.Instance.ProductName, cfg.Instance.ShortName)
		}
	}
}

func TestEnvDefault(t *testing.T) {
	raw := strings.Replace(minimal, "instance:\n", "instance:\n  short_name: \"${ATRIUM_SHORT_NAME:-Atrium}\"\n  organization_name: \"${ATRIUM_ORGANIZATION_NAME:-Example Corp}\"\n", 1)
	base := map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"}
	for name, tc := range map[string]struct {
		extra      map[string]string
		short, org string
	}{
		"both unset":      {nil, "Atrium", "Example Corp"},
		"set":             {map[string]string{"ATRIUM_SHORT_NAME": "Atrium Firma", "ATRIUM_ORGANIZATION_NAME": "Affilicon"}, "Atrium Firma", "Affilicon"},
		"empty is unset":  {map[string]string{"ATRIUM_SHORT_NAME": "", "ATRIUM_ORGANIZATION_NAME": ""}, "Atrium", "Example Corp"},
		"only one is set": {map[string]string{"ATRIUM_ORGANIZATION_NAME": "Privat"}, "Atrium", "Privat"},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range tc.extra {
			m[k] = v
		}
		cfg, err := Parse([]byte(raw), env(m))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Instance.ShortName != tc.short || cfg.Instance.OrganizationName != tc.org {
			t.Errorf("%s: %q / %q", name, cfg.Instance.ShortName, cfg.Instance.OrganizationName)
		}
	}
	// a variable without default is still mandatory
	if _, err := Parse([]byte(minimal), env(nil)); err == nil {
		t.Error("variable without default must stay mandatory")
	}
}

func TestParseMissingEnv(t *testing.T) {
	_, err := Parse([]byte(minimal), env(nil))
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_SECRET") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvInCommentIgnored(t *testing.T) {
	raw := minimal + "\n# client_secret: \"${UNUSED}\"\n"
	if _, err := Parse([]byte(raw), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "x"})); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := Parse([]byte(minimal+"\nbogus: 1\n"), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "x"}))
	if err == nil {
		t.Fatal("unknown field must be rejected")
	}
}

func TestValidate(t *testing.T) {
	good := func() Config {
		c := Defaults()
		c.Instance.BaseURL = "https://k.example.com"
		c.Database.URL = "postgres://x"
		c.Embedding.URL = "http://tei"
		c.Auth.Providers = []Provider{{ID: "g", Type: "google", ClientID: "a", ClientSecret: "b", AllowedDomains: []string{"example.com"}}}
		return c
	}
	cases := []struct {
		name string
		mut  func(*Config)
		want string // empty = valid
	}{
		{"ok", func(*Config) {}, ""},
		{"http prod", func(c *Config) { c.Instance.BaseURL = "http://k.example.com" }, "https"},
		{"http dev localhost", func(c *Config) { devLocal(c) }, ""},
		{"http dev remote", func(c *Config) { c.Instance.DevMode = true; c.Instance.BaseURL = "http://k.example.com" }, "https"},
		{"dev mode on a public https address", func(c *Config) { c.Instance.DevMode = true; c.Instance.DevToken = "0123456789abcdef" }, "localhost"},
		{"dev mode on all interfaces", func(c *Config) { devLocal(c); c.Instance.Listen = ":8080" }, "loopback"},
		{"redirect wildcard without slash", func(c *Config) { c.Auth.AllowedRedirectURIs = []string{"https://chatgpt.com*"} }, "before the *"},
		{"dev mode needs an explicit token", func(c *Config) { devLocal(c); c.Instance.DevToken = "" }, "dev_token"},
		{"no providers", func(c *Config) { c.Auth.Providers = nil }, "Identity Provider"},
		{"no providers dev", func(c *Config) { devLocal(c); c.Auth.Providers = nil }, ""},
		{"negative team quota", func(c *Config) { c.Spaces.Team.MaxDocuments = -1 }, "quotas"},
		{"email_trusted on google", func(c *Config) { c.Auth.Providers[0].EmailTrusted = true }, "email_trusted"},
		{"entra needs tenant", func(c *Config) { c.Auth.Providers[0].Type = "entra" }, "allowed_tenants"},
		{"google without domains or emails", func(c *Config) { c.Auth.Providers[0].AllowedDomains = nil }, "allowed_emails"},
		{"google emails only", func(c *Config) {
			c.Auth.Providers[0].AllowedDomains = nil
			c.Auth.Providers[0].AllowedEmails = []string{"me@gmail.com"}
		}, ""},
		{"entra rejects emails", func(c *Config) {
			c.Auth.Providers[0].Type = "entra"
			c.Auth.Providers[0].AllowedTenants = []string{"t"}
			c.Auth.Providers[0].AllowedEmails = []string{"me@gmail.com"}
		}, "entra"},
		{"simple required", func(c *Config) { c.Search.FTSLanguages = []string{"german"} }, "simple"},
		{"bad proxy", func(c *Config) { c.Instance.TrustedProxies = []string{"nope"} }, "trusted_proxies"},
		{"good proxy", func(c *Config) { c.Instance.TrustedProxies = []string{"10.0.0.0/8", "::1"} }, ""},
		{"dev token without dev mode", func(c *Config) { c.Instance.DevToken = "0123456789abcdef" }, "dev_mode"},
		{"short dev token", func(c *Config) { c.Instance.DevMode = true; c.Instance.DevToken = "short" }, "16"},
		{"dev token ok", func(c *Config) { devLocal(c) }, ""},
		{"openai needs key", func(c *Config) { c.Embedding.Backend = "openai"; c.Embedding.URL = "" }, "api_key"},
		{"openai custom url without key", func(c *Config) { c.Embedding.Backend = "openai" }, ""},
		{"unknown embedding backend", func(c *Config) { c.Embedding.Backend = "ollama" }, "unknown"},
		{"bedrock ok", func(c *Config) {
			c.Embedding.Backend, c.Embedding.URL = "bedrock", ""
			c.Embedding.Model, c.Embedding.Options = "amazon.titan-embed-text-v2:0", map[string]string{"region": "eu-central-1"}
		}, ""},
		{"bedrock wrong dimensions", func(c *Config) {
			c.Embedding.Backend, c.Embedding.URL, c.Embedding.Dimensions = "bedrock", "", 768
			c.Embedding.Model, c.Embedding.Options = "amazon.titan-embed-text-v2:0", map[string]string{"region": "eu-central-1"}
		}, "possible"},
		{"embedding dimensions too large", func(c *Config) { c.Embedding.Dimensions = 3072 }, "2000"},
		{"embedding unknown option", func(c *Config) { c.Embedding.Options = map[string]string{"batchsize": "4"} }, "unknown options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good()
			tc.mut(&c)
			err := c.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// devLocal is a valid dev setup: loopback address and an explicit token.
func devLocal(c *Config) {
	c.Instance.DevMode = true
	c.Instance.Listen = "127.0.0.1:8080"
	c.Instance.BaseURL = "http://localhost:8080"
	c.Instance.DevToken = "0123456789abcdef"
}

// The shipped example must start from the environment alone: that is the promise of a basic setup
// that only fills in .env.
func TestExampleConfigNeedsOnlyEnvironment(t *testing.T) {
	raw, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{
		"ATRIUM_BASE_URL":       "https://atrium.corp.example",
		"ATRIUM_ALLOWED_DOMAIN": "corp.example",
		"ADMIN_EMAIL":           "boss@corp.example",
		"GOOGLE_CLIENT_ID":      "id",
		"GOOGLE_CLIENT_SECRET":  "s",
		"DATABASE_URL":          "postgres://x",
	}
	cfg, err := Parse(raw, env(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance.BaseURL != m["ATRIUM_BASE_URL"] || cfg.Auth.BootstrapAdmins[0] != m["ADMIN_EMAIL"] ||
		cfg.Auth.Providers[0].AllowedDomains[0] != m["ATRIUM_ALLOWED_DOMAIN"] {
		t.Fatalf("values not taken from the environment: %+v", cfg.Instance)
	}
	for _, drop := range []string{"ATRIUM_BASE_URL", "ATRIUM_ALLOWED_DOMAIN", "ADMIN_EMAIL"} {
		short := map[string]string{}
		for k, v := range m {
			if k != drop {
				short[k] = v
			}
		}
		if _, err := Parse(raw, env(short)); err == nil || !strings.Contains(err.Error(), drop) {
			t.Fatalf("without %s the start must fail naming it, got %v", drop, err)
		}
	}
}

func TestSharingIsOffByDefaultAndNeedsPersonalSpaces(t *testing.T) {
	vars := env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"})
	cfg, err := Parse([]byte(minimal), vars)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sharing.Enabled || cfg.Sharing.MaxPendingPerRecipient != 20 || cfg.Sharing.MaxPendingPerSender != 3 {
		t.Errorf("defaults: %+v", cfg.Sharing)
	}
	if _, err := Parse([]byte(minimal+"sharing:\n  enabled: true\nspaces:\n  personal:\n    enabled: false\n"), vars); err == nil || !strings.Contains(err.Error(), "sharing.enabled") {
		t.Errorf("sharing without personal spaces: %v", err)
	}
	if _, err := Parse([]byte(minimal+"sharing:\n  max_pending_per_sender: 0\n"), vars); err == nil || !strings.Contains(err.Error(), "max_pending") {
		t.Errorf("a limit of 0: %v", err)
	}
	if cfg, err := Parse([]byte(minimal+"sharing:\n  enabled: true\n"), vars); err != nil || !cfg.Sharing.Enabled {
		t.Errorf("enabled: %v", err)
	}
}

func TestMigrationURLFromEnvironment(t *testing.T) {
	base := map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://app"}
	cfg, err := Parse([]byte(minimal), env(base))
	if err != nil || cfg.Database.MigrationURL != "" {
		t.Fatalf("without the variable: %q %v", cfg.Database.MigrationURL, err)
	}
	base["DATABASE_MIGRATION_URL"] = "postgres://migrate"
	cfg, err = Parse([]byte(minimal), env(base))
	if err != nil || cfg.Database.MigrationURL != "postgres://migrate" {
		t.Fatalf("from the environment: %q %v", cfg.Database.MigrationURL, err)
	}
	// a value in the file wins
	withFile := strings.Replace(minimal, "database:", "database:\n  migration_url: \"postgres://file\"", 1)
	cfg, err = Parse([]byte(withFile), env(base))
	if err != nil || cfg.Database.MigrationURL != "postgres://file" {
		t.Fatalf("from the file: %q %v", cfg.Database.MigrationURL, err)
	}
}

func TestAuditRetentionNeedsAtLeastThirtyDays(t *testing.T) {
	for days, ok := range map[int]bool{0: true, 7: false, 29: false, 30: true, 365: true, -1: false} {
		cfg := Defaults()
		cfg.Instance.BaseURL = "https://x.example"
		cfg.Audit.RetentionDays = days
		err := cfg.Validate()
		if ok && err != nil && strings.Contains(err.Error(), "audit.retention_days") || !ok && (err == nil || !strings.Contains(err.Error(), "audit.retention_days")) {
			t.Errorf("retention_days=%d: %v", days, err)
		}
	}
}
