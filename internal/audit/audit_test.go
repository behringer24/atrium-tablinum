package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestQueryFiltersAndPrivacy(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	us := users.New(pool, users.Options{PersonalEnabled: true})
	ss := spaces.New(pool)
	anna, _ := us.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "a", Email: "anna@x.de"})
	ben, _ := us.Provision(ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: "b", Email: "ben@x.de"})
	team, _ := ss.CreateTeam(ctx, spaces.CreateParams{Slug: "support", Name: "Support"})
	doc := uuid.New()

	ins := func(ev audit.Event) {
		t.Helper()
		if err := audit.Insert(ctx, pool, ev); err != nil {
			t.Fatal(err)
		}
	}
	ins(audit.Event{UserID: &anna.ID, Client: "claude", Action: "store", SpaceID: &team.ID, DocumentID: &doc, Details: map[string]any{"status": "approved"}})
	ins(audit.Event{UserID: &ben.ID, Client: "chatgpt", Action: "admin_create_space", SpaceID: &team.ID})
	ins(audit.Event{UserID: &ben.ID, Client: "chatgpt", Action: "admin_archive_space", SpaceID: &team.ID})
	// personal space: details are already dropped when writing
	ins(audit.Event{UserID: &anna.ID, Action: "store", SpaceID: anna.PersonalSpaceID, Personal: true, Details: map[string]any{"title": "SECRET"}})

	all, err := audit.Query(ctx, pool, audit.Filter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("%d entries, %v", len(all), err)
	}
	if all[0].Space != audit.PersonalLabel || all[0].SpaceID != nil || all[0].Details != nil {
		t.Errorf("personal entry reveals too much: %+v", all[0])
	}
	if all[3].Details["status"] != "approved" || all[3].UserEmail != "anna@x.de" || all[3].Space != "Support" || all[3].Client != "claude" {
		t.Errorf("team entry: %+v", all[3])
	}

	count := func(f audit.Filter) int {
		t.Helper()
		es, err := audit.Query(ctx, pool, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(es)
	}
	for name, tc := range map[string]struct {
		f    audit.Filter
		want int
	}{
		"user":          {audit.Filter{UserEmail: "ben@x.de"}, 2},
		"exact action":  {audit.Filter{Action: "store"}, 2},
		"action prefix": {audit.Filter{Action: "admin_*"}, 2},
		"space":         {audit.Filter{SpaceID: &team.ID}, 3},
		"document":      {audit.Filter{DocumentID: &doc}, 1},
		"combination":   {audit.Filter{UserEmail: "anna@x.de", Action: "store", SpaceID: &team.ID}, 1},
		"Limit":         {audit.Filter{Limit: 2}, 2},
		"nothing":       {audit.Filter{Action: "doesnotexist"}, 0},
		"limit capped":  {audit.Filter{Limit: 100000}, 4},
	} {
		if got := count(tc.f); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	if count(audit.Filter{Since: &future}) != 0 || count(audit.Filter{Until: &past}) != 0 || count(audit.Filter{Since: &past, Until: &future}) != 4 {
		t.Error("time filter")
	}
}

// A copy of every event goes to the sink as one JSON line; personal spaces keep their details out.
func TestSinkWritesOneLinePerEvent(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	var buf bytes.Buffer
	audit.SetSink(&buf)
	t.Cleanup(func() { audit.SetSink(nil) })

	uid, sid := uuid.New(), uuid.New()
	if err := audit.Insert(ctx, pool, audit.Event{UserID: &uid, Client: "claude", Action: "store", SpaceID: &sid, Details: map[string]any{"status": "approved"}}); err != nil {
		t.Fatal(err)
	}
	if err := audit.Insert(ctx, pool, audit.Event{UserID: &uid, Action: "store", Personal: true, Details: map[string]any{"title": "SECRET"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines: %q", len(lines), buf.String())
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if first["log"] != "audit" || first["action"] != "store" || first["client"] != "claude" || first["user_id"] != uid.String() || first["space_id"] != sid.String() {
		t.Errorf("first line: %v", first)
	}
	if first["details"].(map[string]any)["status"] != "approved" {
		t.Errorf("details missing: %v", first)
	}
	if _, ok := second["details"]; ok || strings.Contains(lines[1], "SECRET") {
		t.Errorf("a personal event must not carry details: %s", lines[1])
	}

	// off again: nothing more is written
	audit.SetSink(nil)
	buf.Reset()
	_ = audit.Insert(ctx, pool, audit.Event{Action: "login"})
	if buf.Len() != 0 {
		t.Errorf("written after SetSink(nil): %q", buf.String())
	}
}

// Retention removes old entries, records that it did, and never touches the last 30 days.
func TestPurgeKeepsRecentEntries(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	for _, a := range []string{"old1", "old2", "recent"} {
		if err := audit.Insert(ctx, pool, audit.Event{Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET ts = now() - interval '100 days' WHERE action LIKE 'old%'`); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Purge(ctx, pool, 7*24*time.Hour); err == nil {
		t.Error("a retention of seven days must be refused")
	}
	n, err := audit.Purge(ctx, pool, 90*24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
	got, _ := audit.Query(ctx, pool, audit.Filter{})
	actions := map[string]bool{}
	for _, e := range got {
		actions[e.Action] = true
	}
	if len(got) != 2 || !actions["recent"] || !actions["audit_purge"] {
		t.Errorf("after the purge: %+v", got)
	}
}
