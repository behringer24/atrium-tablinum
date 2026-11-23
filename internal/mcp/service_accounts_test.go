package mcpserver

import (
	"strings"
	"testing"
)

// Service accounts are marked in the user list and cannot be made instance admins.
func TestServiceAccountInTheUserTools(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	e.session(t, "anna@x.de")
	if _, err := e.srv.Pool.Exec(e.ctx,
		`INSERT INTO users (email, idp, idp_subject, kind) VALUES ('docs@service.invalid', 'service', 'docs', 'service')`); err != nil {
		t.Fatal(err)
	}

	out := e.mustCall(t, boss, "admin_list_users", nil)
	kinds := map[string]any{}
	for _, u := range out["users"].([]any) {
		row := u.(map[string]any)
		kinds[row["email"].(string)] = row["kind"]
	}
	if kinds["docs@service.invalid"] != "service" {
		t.Errorf("kind of the service account: %v", kinds["docs@service.invalid"])
	}
	if _, ok := kinds["anna@x.de"]; !ok || kinds["anna@x.de"] != nil {
		t.Errorf("a person must show no kind: %v", kinds["anna@x.de"])
	}

	_, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "grant", "user_email": "docs@service.invalid", "confirm": true})
	if !strings.Contains(msg, "service account") {
		t.Errorf("granting the admin right to a service account: %q", msg)
	}
	list := e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "list"})
	for _, a := range list["admins"].([]any) {
		if a.(map[string]any)["email"] == "docs@service.invalid" {
			t.Error("the service account is listed as an instance admin")
		}
	}
}
