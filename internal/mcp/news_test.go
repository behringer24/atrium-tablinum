package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// whats_new reports what readers can see that is new or changed, and the waiting drafts to curators.
func TestWhatsNew(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader,
	})
	e.team(t, "hr", "HR", true, map[string]authz.Role{"anna@x.de": authz.Author, "cora@x.de": authz.Curator})
	anna, cora, bob, dora := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "dora@x.de")

	store := func(title, space string) string {
		t.Helper()
		out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": title, "content": title + " in detail", "space": space, "force": true})
		return out["id"].(string)
	}
	age := func(id string) {
		t.Helper()
		if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE documents SET created_at = now() - interval '30 days', updated_at = now() - interval '30 days' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE document_versions SET changed_at = now() - interval '30 days' WHERE document_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	changed := store("Deploy steps", "dev")
	age(changed)
	age(store("Old coding rules", "dev"))
	e.mustCall(t, anna, "update_knowledge", map[string]any{"id": changed, "content": "Deploy steps, now with a canary stage"})
	store("Rollback steps", "dev")
	store("Severance terms", "hr") // a draft: the space requires approval
	e.mustCall(t, bob, "store_knowledge", map[string]any{"title": "My own notes", "content": "private", "force": true})
	e.releaseSkill(t, anna, cora, "code-review", nil)

	titles := func(out map[string]any) map[string]string {
		got := map[string]string{}
		for _, x := range out["entries"].([]any) {
			m := x.(map[string]any)
			got[m["title"].(string)] = m["change"].(string)
		}
		return got
	}
	out := e.mustCall(t, bob, "whats_new", nil)
	if got := titles(out); len(got) != 2 || got["Rollback steps"] != "new" || got["Deploy steps"] != "updated" {
		t.Errorf("reader, last 7 days: %v", got)
	}
	if sk := out["skills"].([]any); len(sk) != 1 || sk[0].(map[string]any)["name"] != "code-review" {
		t.Errorf("released skills: %v", sk)
	}
	if out["waiting_for_review"] != nil {
		t.Errorf("a reader sees the review queue: %v", out["waiting_for_review"])
	}

	// further back, the entry nobody changed since appears as new; the date is a calendar day
	since := time.Now().AddDate(0, 0, -31).Format("2006-01-02")
	if got := titles(e.mustCall(t, bob, "whats_new", map[string]any{"since": since})); len(got) != 3 || got["Old coding rules"] != "new" {
		t.Errorf("reader, 31 days: %v", got)
	}

	// the curator learns what waits in their spaces, without seeing the draft as news
	out = e.mustCall(t, cora, "whats_new", nil)
	if _, ok := titles(out)["Severance terms"]; ok {
		t.Error("a draft is reported as news")
	}
	waits := out["waiting_for_review"].([]any)
	if len(waits) != 1 || waits[0].(map[string]any)["space"] != "HR" || waits[0].(map[string]any)["entries"] != float64(1) {
		t.Errorf("waiting for review: %v", waits)
	}

	// somebody without access sees nothing, and cannot ask for a space they cannot read
	out = e.mustCall(t, dora, "whats_new", nil)
	if len(out["entries"].([]any)) != 0 || len(out["skills"].([]any)) != 0 || !strings.Contains(out["message"].(string), "Nothing new") {
		t.Errorf("outsider: %v", out)
	}
	if _, msg := e.call(t, bob, "whats_new", map[string]any{"spaces": []string{"hr"}}); !strings.Contains(msg, "not found") {
		t.Errorf("space without access: %q", msg)
	}
	for _, bad := range []string{"tomorrow", "2999-01-01"} {
		if _, msg := e.call(t, bob, "whats_new", map[string]any{"since": bad}); msg == "" {
			t.Errorf("since %q accepted", bad)
		}
	}
	if got := titles(e.mustCall(t, anna, "whats_new", map[string]any{"spaces": []string{"dev"}, "limit": 1})); len(got) != 1 {
		t.Errorf("limit: %v", got)
	}
}
