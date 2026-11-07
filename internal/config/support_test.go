package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// The summary for support shows settings, never secrets, addresses of other systems or names.
func TestSupportSummaryLeaksNothing(t *testing.T) {
	c := Defaults()
	c.Instance.BaseURL = "https://atrium.example.com/"
	c.Instance.OrganizationName = "Secret Project GmbH"
	c.Instance.DevToken = "dev-token-value"
	c.Auth.BootstrapAdmins = []string{"boss@example.com"}
	c.Auth.Providers = []Provider{{ID: "corp", Type: "oidc", Issuer: "https://login.internal.example.net/tenant",
		ClientID: "client-id-value", ClientSecret: "client-secret-value", AllowedDomains: []string{"customer.example"}}}
	c.Database.URL = "postgres://atrium:db-password-value@db.internal:5432/atrium"
	c.Database.MigrationURL = "postgres://owner:owner-password-value@db.internal:5432/atrium"
	c.Embedding.URL = "http://embedder.internal:8080"
	c.Embedding.APIKey = "api-key-value"
	c.Embedding.Options = map[string]string{"region": "eu-central-1", "token": "option-secret-value"}
	c.Reranker.URL = "http://reranker.internal:80"
	c.Spaces.DefaultSpaceByGr = map[string]string{"hr-team": "hr"}
	c.Tools.Descriptions = map[string]string{"store_knowledge": "Our secret wording"}

	b, err := json.Marshal(c.SupportSummary())
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, secret := range []string{"Secret Project", "dev-token-value", "boss@example.com", "login.internal", "client-id-value",
		"client-secret-value", "customer.example", "db-password-value", "owner-password-value", "db.internal", "embedder.internal",
		"api-key-value", "eu-central-1", "option-secret-value", "reranker.internal", "hr-team", "secret wording"} {
		if strings.Contains(out, secret) {
			t.Errorf("the support summary shows %q: %s", secret, out)
		}
	}
	for _, want := range []string{`"host":"atrium.example.com"`, `"providers":["oidc"]`, `"separate_migration_account":true`, `"options":["region","token"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("the support summary misses %s: %s", want, out)
		}
	}
}
