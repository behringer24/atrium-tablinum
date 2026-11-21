package mcpserver

import (
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

func setParents(t *testing.T, e *env, pairs map[string]string) {
	t.Helper()
	for child, parent := range pairs {
		if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE spaces SET parent_id = (SELECT id FROM spaces WHERE slug = $2) WHERE slug = $1`, child, parent); err != nil {
			t.Fatal(err)
		}
	}
	e.srv.Checker.InvalidateAll()
}

func memberRows(out map[string]any, key string) map[string]map[string]any {
	rows := map[string]map[string]any{}
	list, _ := out[key].([]any)
	for _, r := range list {
		m := r.(map[string]any)
		rows[m["name"].(string)] = m
	}
	return rows
}

// space_members and space_contacts show what a space takes over from the spaces above, like the rights
// report does.
func TestMemberListsShowInheritedMemberships(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "marketing", "Marketing", false, map[string]authz.Role{"anna@x.de": authz.Admin, "ben@x.de": authz.Reader})
	e.team(t, "kampagnen", "Kampagnen", false, map[string]authz.Role{"carla@x.de": authz.Admin, "ben@x.de": authz.Author})
	e.team(t, "messe", "Messe", false, map[string]authz.Role{"dora@x.de": authz.Reader})
	setParents(t, e, map[string]string{"kampagnen": "marketing", "messe": "kampagnen"})
	boss := e.session(t, "boss@x.de")
	carla := e.session(t, "carla@x.de")
	dora := e.session(t, "dora@x.de")

	// A space admin sees who has access to the space through the spaces above. Ben has an entry of his
	// own in kampagnen, so his reader entry in marketing is not listed. Carla cannot see marketing and
	// is not told its name.
	out := e.mustCall(t, carla, "space_members", map[string]any{"space": "kampagnen"})
	if out["inherit_members"] != true {
		t.Errorf("inherit_members: %v", out["inherit_members"])
	}
	inh := memberRows(out, "inherited_members")
	if len(inh) != 1 || inh["anna@x.de"]["role"] != "admin" || inh["anna@x.de"]["from"] != "a space above" {
		t.Errorf("inherited members seen by a space admin: %v", inh)
	}
	if own := memberRows(out, "members"); len(own) != 2 || own["ben@x.de"]["role"] != "author" {
		t.Errorf("own members: %v", own)
	}

	// An instance admin sees where they come from.
	inh = memberRows(e.mustCall(t, boss, "space_members", map[string]any{"space": "messe"}), "inherited_members")
	if inh["carla@x.de"]["from"] != "kampagnen" || inh["anna@x.de"]["from"] != "marketing" || inh["ben@x.de"]["role"] != "author" || inh["ben@x.de"]["from"] != "kampagnen" {
		t.Errorf("inherited members of messe: %v", inh)
	}

	// A top-level space shows nothing of the kind.
	top := e.mustCall(t, boss, "space_members", map[string]any{"space": "marketing"})
	for _, key := range []string{"inherit_members", "inherited_members"} {
		if _, ok := top[key]; ok {
			t.Errorf("top-level space has %q", key)
		}
	}

	// A reader of messe finds the curators and admins to ask in the spaces above, not Ben (an author).
	contacts := func() map[string]map[string]any {
		res := e.mustCall(t, dora, "space_contacts", map[string]any{"space": "messe"})
		rows := map[string]map[string]any{}
		for _, sp := range res["spaces"].([]any) {
			for _, c := range sp.(map[string]any)["contacts"].([]any) {
				m := c.(map[string]any)
				rows[m["name"].(string)] = m
			}
		}
		return rows
	}
	got := contacts()
	if len(got) != 2 || got["carla@x.de"]["role"] != "admin" || got["anna@x.de"]["role"] != "admin" {
		t.Errorf("contacts of messe: %v", got)
	}
	for name, c := range got {
		if _, ok := c["from"]; ok {
			t.Errorf("%s: a contact names the space it comes from", name)
		}
	}

	// Without the inheritance there is nothing inherited, in either list.
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE spaces SET inherit_members = false WHERE slug = 'messe'`); err != nil {
		t.Fatal(err)
	}
	e.srv.Checker.InvalidateAll()
	if got := contacts(); len(got) != 0 {
		t.Errorf("contacts after inherit_members=false: %v", got)
	}
	out = e.mustCall(t, boss, "space_members", map[string]any{"space": "messe"})
	if out["inherit_members"] != false || len(memberRows(out, "inherited_members")) != 0 {
		t.Errorf("space_members after inherit_members=false: %v", out)
	}
}
