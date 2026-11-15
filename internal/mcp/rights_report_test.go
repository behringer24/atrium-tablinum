package mcpserver

import (
	"strings"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

func TestRightsReport(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "technik", "Technik", true, map[string]authz.Role{"anna@x.de": authz.Admin, "ben@x.de": authz.Author})
	e.team(t, "verwaist", "Verwaist", false, map[string]authz.Role{"ben@x.de": authz.Reader})
	boss := e.session(t, "boss@x.de")
	e.mustCall(t, boss, "admin_create_space", map[string]any{"slug": "offen", "name": "Offen", "default_role": "reader"})
	e.mustCall(t, boss, "admin_groups", map[string]any{"action": "create", "name": "Team"})
	e.mustCall(t, boss, "space_add_member", map[string]any{"space": "offen", "group": "Team", "role": "admin"})

	out := e.mustCall(t, boss, "admin_rights_report", nil)
	if out["admins_read_content"] != true {
		t.Errorf("admins_read_content: %v", out["admins_read_content"])
	}
	if admins := out["instance_admins"].([]any); len(admins) != 1 || admins[0].(map[string]any)["email"] != "boss@x.de" {
		t.Errorf("instance admins: %v", admins)
	}
	bySlug := map[string]map[string]any{}
	for _, sp := range out["spaces"].([]any) {
		bySlug[sp.(map[string]any)["slug"].(string)] = sp.(map[string]any)
	}
	if len(bySlug) != 3 {
		t.Fatalf("spaces: %v", out["spaces"])
	}
	tech := bySlug["technik"]
	if tech["requires_approval"] != true || tech["default_role"] != "none" || tech["open_to_everyone"] == true {
		t.Errorf("technik: %v", tech)
	}
	holders := tech["holders"].([]any)
	if len(holders) != 2 || holders[0].(map[string]any)["name"] != "anna@x.de" || holders[0].(map[string]any)["role"] != "admin" {
		t.Errorf("holders of technik (highest role first): %v", holders)
	}
	if offen := bySlug["offen"]; offen["open_to_everyone"] != true || offen["default_role"] != "reader" {
		t.Errorf("offen: %v", offen)
	}
	var findings []string
	for _, f := range out["findings"].([]any) {
		findings = append(findings, f.(string))
	}
	all := strings.Join(findings, "\n")
	for _, want := range []string{"verwaist: no space admin", "offen: a group holds the admin role", "offen: every logged-in user may read"} {
		if !strings.Contains(all, want) {
			t.Errorf("finding %q missing in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "technik") {
		t.Errorf("technik is fine but reported:\n%s", all)
	}

	// a locked account keeps its membership, marked, and no longer counts as admin
	e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "anna@x.de", "confirm": true})
	out = e.mustCall(t, boss, "admin_rights_report", nil)
	if !strings.Contains(strings.Join(func() []string {
		var s []string
		for _, f := range out["findings"].([]any) {
			s = append(s, f.(string))
		}
		return s
	}(), "\n"), "technik: no space admin") {
		t.Errorf("a locked space admin still counts: %v", out["findings"])
	}
}
