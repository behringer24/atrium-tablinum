package mcpserver

import (
	"strings"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

func reportSpaces(out map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, sp := range out["spaces"].([]any) {
		row := sp.(map[string]any)
		m[row["slug"].(string)] = row
	}
	return m
}

func reportFindings(out map[string]any) string {
	var s []string
	for _, f := range out["findings"].([]any) {
		s = append(s, f.(string))
	}
	return strings.Join(s, "\n")
}

func holderRows(row map[string]any, key string) []map[string]any {
	var out []map[string]any
	list, _ := row[key].([]any)
	for _, h := range list {
		out = append(out, h.(map[string]any))
	}
	return out
}

// A flat instance gets the report it always got: none of the fields of the hierarchy appear.
func TestRightsReportFlatHasNoHierarchy(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "technik", "Technik", false, map[string]authz.Role{"anna@x.de": authz.Admin})
	boss := e.session(t, "boss@x.de")
	out := e.mustCall(t, boss, "admin_rights_report", nil)
	for slug, row := range reportSpaces(out) {
		for _, key := range []string{"parent", "inherit_members", "inherited_holders"} {
			if _, ok := row[key]; ok {
				t.Errorf("%s: flat report has %q", slug, key)
			}
		}
	}
	if strings.Contains(out["note"].(string), "inherit") {
		t.Errorf("note of a flat report: %v", out["note"])
	}
}

func TestRightsReportShowsInheritedRoles(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "marketing", "Marketing", false, map[string]authz.Role{"anna@x.de": authz.Admin, "ben@x.de": authz.Reader})
	e.team(t, "kampagnen", "Kampagnen", false, map[string]authz.Role{"ben@x.de": authz.Author})
	e.team(t, "messe", "Messe", false, nil)
	boss := e.session(t, "boss@x.de")
	for child, parent := range map[string]string{"kampagnen": "marketing", "messe": "kampagnen"} {
		if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE spaces SET parent_id = (SELECT id FROM spaces WHERE slug = $2) WHERE slug = $1`, child, parent); err != nil {
			t.Fatal(err)
		}
	}

	out := e.mustCall(t, boss, "admin_rights_report", nil)
	if !strings.Contains(out["note"].(string), "inherit") {
		t.Errorf("the note does not mention the inheritance: %v", out["note"])
	}
	spaces := reportSpaces(out)
	if _, ok := spaces["marketing"]["parent"]; ok {
		t.Errorf("a top-level space shows a parent: %v", spaces["marketing"])
	}

	// kampagnen: Anna's admin role comes from marketing; Ben has an entry of his own, so his reader
	// entry above is not listed again.
	k := spaces["kampagnen"]
	if k["parent"] != "marketing" || k["inherit_members"] != true {
		t.Errorf("kampagnen: %v", k)
	}
	inh := holderRows(k, "inherited_holders")
	if len(inh) != 1 || inh[0]["name"] != "anna@x.de" || inh[0]["role"] != "admin" || inh[0]["from"] != "marketing" {
		t.Errorf("inherited holders of kampagnen: %v", inh)
	}

	// messe: the nearest entry of Ben is the author entry in kampagnen, Anna's admin comes from marketing.
	inh = holderRows(spaces["messe"], "inherited_holders")
	if len(inh) != 2 || inh[0]["name"] != "anna@x.de" || inh[0]["from"] != "marketing" || inh[1]["name"] != "ben@x.de" || inh[1]["role"] != "author" || inh[1]["from"] != "kampagnen" {
		t.Errorf("inherited holders of messe: %v", inh)
	}

	// An admin that is only inherited is an admin: no "no space admin", but a hint where it comes from.
	findings := reportFindings(out)
	if strings.Contains(findings, "kampagnen: no space admin") || strings.Contains(findings, "messe: no space admin") {
		t.Errorf("an inherited admin was not counted: %v", findings)
	}
	if !strings.Contains(findings, "messe: the admin role of anna@x.de (marketing) is inherited") {
		t.Errorf("no hint about the inherited admin: %v", findings)
	}

	// Stopping the inheritance in messe shows it as it is: nothing inherited, no admin.
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE spaces SET inherit_members = false WHERE slug = 'messe'`); err != nil {
		t.Fatal(err)
	}
	out = e.mustCall(t, boss, "admin_rights_report", nil)
	m := reportSpaces(out)["messe"]
	if m["inherit_members"] != false || len(holderRows(m, "inherited_holders")) != 0 {
		t.Errorf("messe without inheritance: %v", m)
	}
	if !strings.Contains(reportFindings(out), "messe: no space admin") {
		t.Errorf("no finding for a space without admin: %v", reportFindings(out))
	}
}
