package audit_test

import (
	"context"
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
	anna, _ := us.Provision(ctx, users.Identity{IDP: "g", Subject: "a", Email: "anna@x.de"})
	ben, _ := us.Provision(ctx, users.Identity{IDP: "g", Subject: "b", Email: "ben@x.de"})
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
	ins(audit.Event{UserID: &anna.ID, Action: "store", SpaceID: anna.PersonalSpaceID, Personal: true, Details: map[string]any{"title": "GEHEIM"}})

	all, err := audit.Query(ctx, pool, audit.Filter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("%d Einträge, %v", len(all), err)
	}
	if all[0].Space != audit.PersonalLabel || all[0].SpaceID != nil || all[0].Details != nil {
		t.Errorf("persönlicher Eintrag verrät zu viel: %+v", all[0])
	}
	if all[3].Details["status"] != "approved" || all[3].UserEmail != "anna@x.de" || all[3].Space != "Support" || all[3].Client != "claude" {
		t.Errorf("Team-Eintrag: %+v", all[3])
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
		"Nutzer":          {audit.Filter{UserEmail: "ben@x.de"}, 2},
		"Aktion exakt":    {audit.Filter{Action: "store"}, 2},
		"Aktion Präfix":   {audit.Filter{Action: "admin_*"}, 2},
		"Bereich":         {audit.Filter{SpaceID: &team.ID}, 3},
		"Dokument":        {audit.Filter{DocumentID: &doc}, 1},
		"Kombination":     {audit.Filter{UserEmail: "anna@x.de", Action: "store", SpaceID: &team.ID}, 1},
		"Limit":           {audit.Filter{Limit: 2}, 2},
		"nichts":          {audit.Filter{Action: "gibtsnicht"}, 0},
		"Limit gedeckelt": {audit.Filter{Limit: 100000}, 4},
	} {
		if got := count(tc.f); got != tc.want {
			t.Errorf("%s: %d, erwartet %d", name, got, tc.want)
		}
	}
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	if count(audit.Filter{Since: &future}) != 0 || count(audit.Filter{Until: &past}) != 0 || count(audit.Filter{Since: &past, Until: &future}) != 4 {
		t.Error("Zeitfilter")
	}
}
