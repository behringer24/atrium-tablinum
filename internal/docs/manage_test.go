package docs_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
)

func TestMoveNeedsWriteAccessInTarget(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	writeSp := e.team(t, "write", false, authz.None)
	readSp := e.team(t, "read", false, authz.None)
	hidden := e.team(t, "hidden", false, authz.None)
	e.member(t, writeSp, anna, authz.Author)
	e.member(t, readSp, anna, authz.Reader)
	anna = e.refresh(t, anna)

	d := mustStore(t, e, anna, personal(anna), "Notiz", "Inhalt alpha")
	chunks := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1`, d.ID)

	if _, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: readSp.ID}); !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("target read-only: %v", err)
	}
	if _, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: hidden.ID}); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("target invisible: %v", err)
	}
	if _, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: uuid.New()}); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("target unknown: %v", err)
	}
	if _, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: personal(anna)}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("same space: %v", err)
	}
	if got, _ := e.docs.Get(e.ctx, anna, d.ID); got.SpaceID != personal(anna) {
		t.Fatal("failed moves changed the entry")
	}

	moved, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: writeSp.ID})
	if err != nil {
		t.Fatal(err)
	}
	if moved.SpaceID != writeSp.ID || moved.Version != d.Version+1 || moved.Status != "approved" {
		t.Errorf("%+v", moved)
	}
	if list, _ := e.docs.List(e.ctx, anna, docs.ListParams{SpaceID: personal(anna)}); len(list) != 0 {
		t.Errorf("entry still in old space: %d", len(list))
	}
	if list, _ := e.docs.List(e.ctx, anna, docs.ListParams{SpaceID: writeSp.ID}); len(list) != 1 {
		t.Errorf("entry not in target space: %d", len(list))
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1`, d.ID); n != chunks {
		t.Errorf("Chunks %d -> %d", chunks, n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM document_versions WHERE document_id = $1 AND space_id = $2`, d.ID, writeSp.ID); n != 1 {
		t.Errorf("version count in target space: %d", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'move' AND document_id = $1`, d.ID); n != 1 {
		t.Errorf("audit entries: %d", n)
	}
}

func TestMoveRespectsEditRightsAndApproval(t *testing.T) {
	e := setup(t)
	author := e.actor(t, "author@x.de")
	other := e.actor(t, "other@x.de")
	curator := e.actor(t, "cur@x.de")
	open := e.team(t, "open", false, authz.None)
	review := e.team(t, "review", true, authz.None)
	for _, a := range []docs.Actor{author, other, curator} {
		e.member(t, open, a, authz.Author)
	}
	e.member(t, review, author, authz.Author)
	e.member(t, review, curator, authz.Curator)
	author, other, curator = e.refresh(t, author), e.refresh(t, other), e.refresh(t, curator)

	d := mustStore(t, e, author, open.ID, "Eintrag", "Inhalt alpha")

	// an author may not move other people's entries
	if _, err := e.docs.Move(e.ctx, other, docs.MoveParams{ID: d.ID, SpaceID: review.ID}); !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("other's entry: %v", err)
	}
	// into a space with approval required: becomes a draft
	moved, err := e.docs.Move(e.ctx, author, docs.MoveParams{ID: d.ID, SpaceID: review.ID})
	if err != nil || moved.Status != "draft" {
		t.Fatalf("%+v %v", moved, err)
	}
	// drafts cannot be moved
	if _, err := e.docs.Move(e.ctx, author, docs.MoveParams{ID: d.ID, SpaceID: open.ID}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("draft: %v", err)
	}
	// a curator may move into an approval space without draft status
	d2 := mustStore(t, e, curator, open.ID, "Zweiter", "Inhalt beta")
	moved, err = e.docs.Move(e.ctx, curator, docs.MoveParams{ID: d2.ID, SpaceID: review.ID})
	if err != nil || moved.Status != "approved" {
		t.Fatalf("%+v %v", moved, err)
	}
	// an author cannot take approved entries out of an approval space
	d3 := mustStore(t, e, author, review.ID, "Dritter", "Inhalt alpha beta")
	if d3.Status != "draft" {
		t.Fatalf("Status = %s", d3.Status)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE documents SET status = 'approved' WHERE id = $1`, d3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.Move(e.ctx, author, docs.MoveParams{ID: d3.ID, SpaceID: open.ID}); !errors.Is(err, docs.ErrNeedsApproval) {
		t.Errorf("author moves approved entry out: %v", err)
	}
}

func TestMoveIntoPersonalSpaceChecksQuota(t *testing.T) {
	e := setup(t) // quota: 3 entries
	anna := e.actor(t, "anna@x.de")
	sp := e.team(t, "team", false, authz.None)
	e.member(t, sp, anna, authz.Author)
	anna = e.refresh(t, anna)
	for _, title := range []string{"a", "b", "c"} {
		mustStore(t, e, anna, personal(anna), title, "x")
	}
	d := mustStore(t, e, anna, sp.ID, "Team", "x")
	if _, err := e.docs.Move(e.ctx, anna, docs.MoveParams{ID: d.ID, SpaceID: personal(anna)}); !errors.Is(err, docs.ErrQuota) {
		t.Errorf("quota: %v", err)
	}
}

func TestDeleteIsAdminOnly(t *testing.T) {
	e := setup(t)
	author := e.actor(t, "author@x.de")
	curator := e.actor(t, "cur@x.de")
	spaceAdmin := e.actor(t, "sadmin@x.de")
	boss := e.actor(t, "boss@x.de") // bootstrap instance admin
	outsider := e.actor(t, "out@x.de")
	sp := e.team(t, "wiki", false, authz.None)
	e.member(t, sp, author, authz.Author)
	e.member(t, sp, curator, authz.Curator)
	e.member(t, sp, spaceAdmin, authz.Admin)
	author, curator, spaceAdmin, boss = e.refresh(t, author), e.refresh(t, curator), e.refresh(t, spaceAdmin), e.refresh(t, boss)

	d := mustStore(t, e, author, sp.ID, "Wichtig", "Inhalt alpha")

	for name, a := range map[string]docs.Actor{"Author (Ersteller)": author, "Kurator": curator} {
		if err := e.docs.Delete(e.ctx, a, d.ID); !errors.Is(err, docs.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.docs.Delete(e.ctx, outsider, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("outsider: %v", err)
	}
	if _, err := e.docs.Get(e.ctx, author, d.ID); err != nil {
		t.Fatalf("entry gone despite rejected deletion: %v", err)
	}

	if err := e.docs.Delete(e.ctx, spaceAdmin, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.Get(e.ctx, author, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("entry still readable: %v", err)
	}
	for _, table := range []string{"chunks", "document_versions"} {
		if n := queryInt(t, e, `SELECT count(*) FROM `+table+` WHERE document_id = $1`, d.ID); n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
	// title is in the audit log of the team space
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'delete' AND details->>'title' = 'Wichtig'`); n != 1 {
		t.Errorf("audit with title: %d", n)
	}

	// an instance admin may delete in every team space
	d2 := mustStore(t, e, author, sp.ID, "Zweiter", "Inhalt beta")
	if err := e.docs.Delete(e.ctx, boss, d2.ID); err != nil {
		t.Errorf("instance admin: %v", err)
	}
}

func TestDeleteInPersonalSpace(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	boss := e.actor(t, "boss@x.de")
	d := mustStore(t, e, anna, personal(anna), "Privat", "Inhalt alpha")

	// even instance admins cannot reach other people's personal entries
	if err := e.docs.Delete(e.ctx, boss, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("instance admin: %v", err)
	}
	if err := e.docs.Delete(e.ctx, anna, d.ID); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'delete' AND details IS NOT NULL`); n != 0 {
		t.Errorf("personal deletion left details in the audit log")
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'delete'`); n != 1 {
		t.Errorf("audit entries: %d", n)
	}
}
