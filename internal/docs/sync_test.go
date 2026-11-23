package docs_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
)

// serviceActor adds a service account directly (nothing in this edition creates one) and gives it a role in
// the space.
func (e env) serviceActor(t *testing.T, name string, sp uuid.UUID, role authz.Role) docs.Actor {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO users (email, idp, idp_subject, kind) VALUES ($1, 'service', $2, 'service') RETURNING id`,
		name+"@service.invalid", name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := e.spaces.SetMember(e.ctx, sp, "user", id, role, id); err != nil {
		t.Fatal(err)
	}
	u, err := e.users.Get(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := e.actorFor(t, u)
	a.Client = "token:" + name
	return a
}

func syncParams(space uuid.UUID, key, content, sha string) docs.SyncParams {
	return docs.SyncParams{SpaceID: space, Key: key, Title: "Doc " + key, Content: content, Tags: []string{"code"},
		Origin: docs.Origin{Ref: "git://repo/" + key, SHA256: strings.Repeat(sha, 64)}}
}

func TestSyncCreatesUpdatesAndLeavesAlone(t *testing.T) {
	e := setup(t)
	sp := e.team(t, "code", false, authz.None)
	svc := e.serviceActor(t, "docs-sync", sp.ID, authz.Author)

	res, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "a.md", "Text of the first file.", "1"))
	if err != nil || res.Action != docs.SyncCreated {
		t.Fatalf("create: %+v %v", res, err)
	}
	d := res.Document
	if d.SyncKey != "a.md" || d.ManagedBy == nil || *d.ManagedBy != svc.User.ID || d.ManagedByEmail != "docs-sync@service.invalid" || d.Status != "approved" {
		t.Errorf("created entry: %+v", d)
	}
	if len(d.Origins) != 1 || d.Origins[0].Ref != "git://repo/a.md" || d.Version != 1 {
		t.Errorf("origin or version: %+v", d)
	}

	// The same source again changes nothing, not even the version.
	res, err = e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "a.md", "Text of the first file.", "1"))
	if err != nil || res.Action != docs.SyncUnchanged || res.Document.Version != 1 {
		t.Errorf("unchanged: %+v %v", res, err)
	}

	// New text: a new version of the same entry, not a second entry.
	res, err = e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "a.md", "Text of the first file, changed.", "2"))
	if err != nil || res.Action != docs.SyncUpdated || res.Document.Version != 2 || res.Document.ID != d.ID {
		t.Errorf("update: %+v %v", res, err)
	}
	if got := queryInt(t, e, `SELECT count(*) FROM documents WHERE sync_key = 'a.md'`); got != 1 {
		t.Errorf("%d entries with the key", got)
	}

	// Only the origin differs (the file changed in a way that does not touch the text): no new version, but the
	// hash is kept.
	res, err = e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "a.md", "Text of the first file, changed.", "3"))
	if err != nil || res.Action != docs.SyncUpdated || res.Document.Version != 2 || res.Document.Origins[0].SHA256 != strings.Repeat("3", 64) {
		t.Errorf("origin only: %+v %v", res, err)
	}

	// Two keys may hold the same text: the key is the identity, not the similarity.
	if _, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "copy.md", "Text of the first file, changed.", "3")); err != nil {
		t.Errorf("same text under another key: %v", err)
	}
}

func TestSyncEntriesBelongToTheirAccount(t *testing.T) {
	e := setup(t)
	sp := e.team(t, "code", false, authz.None)
	svc := e.serviceActor(t, "docs-sync", sp.ID, authz.Curator)
	other := e.serviceActor(t, "other-sync", sp.ID, authz.Curator)
	curator := e.actor(t, "cora@x.de")
	e.member(t, sp, curator, authz.Curator)
	curator = e.refresh(t, curator)
	dst := e.team(t, "target", false, authz.None)
	e.member(t, dst, curator, authz.Curator)
	curator = e.refresh(t, curator)

	res, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "a.md", "Text of the file.", "1"))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Document.ID

	// Nobody else changes or moves it, curators of the space included.
	newText := "A person changes it."
	if _, err := e.docs.Update(e.ctx, curator, docs.UpdateParams{ID: id, Content: &newText}); !errors.Is(err, docs.ErrManaged) {
		t.Errorf("update by a curator: %v", err)
	}
	if _, err := e.docs.Move(e.ctx, curator, docs.MoveParams{ID: id, SpaceID: dst.ID}); !errors.Is(err, docs.ErrManaged) {
		t.Errorf("move by a curator: %v", err)
	}
	// Another tool does not take it over through the key either.
	if _, err := e.docs.Sync(e.ctx, other, syncParams(sp.ID, "a.md", "Another tool writes.", "2")); !errors.Is(err, docs.ErrManaged) {
		t.Errorf("sync by another account: %v", err)
	}
	// Its own account can change it with update_knowledge as well.
	if _, err := e.docs.Update(e.ctx, svc, docs.UpdateParams{ID: id, Content: &newText}); err != nil {
		t.Errorf("update by the account itself: %v", err)
	}
	// Archiving stays possible for a curator, so that nobody is stuck with a wrong entry.
	if err := e.docs.Archive(e.ctx, curator, id); err != nil {
		t.Errorf("archive by a curator: %v", err)
	}
}

func TestSyncListAndArchive(t *testing.T) {
	e := setup(t)
	sp := e.team(t, "code", false, authz.None)
	svc := e.serviceActor(t, "docs-sync", sp.ID, authz.Author)
	other := e.serviceActor(t, "other-sync", sp.ID, authz.Author)
	for _, k := range []string{"repo:docs/a.md", "repo:docs/b.md", "repo:src/c.md"} {
		if _, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, k, "Text of "+k, "1")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.docs.Sync(e.ctx, other, syncParams(sp.ID, "other:x.md", "Text of x", "1")); err != nil {
		t.Fatal(err)
	}

	list, err := e.docs.SyncList(e.ctx, svc, sp.ID, "repo:docs/")
	if err != nil || len(list) != 2 || list[0].Key != "repo:docs/a.md" || list[1].Key != "repo:docs/b.md" || list[0].SHA256 != strings.Repeat("1", 64) {
		t.Fatalf("list by prefix: %+v %v", list, err)
	}
	if all, _ := e.docs.SyncList(e.ctx, svc, sp.ID, ""); len(all) != 3 {
		t.Errorf("all of the account: %d, expected 3 (the entry of the other account is not listed)", len(all))
	}

	// A source that is gone: archive the entry; it is no longer listed, and archiving it twice finds nothing.
	if err := e.docs.SyncArchive(e.ctx, svc, sp.ID, "repo:docs/b.md"); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.docs.SyncList(e.ctx, svc, sp.ID, "repo:docs/"); len(list) != 1 {
		t.Errorf("after archiving: %+v", list)
	}
	if err := e.docs.SyncArchive(e.ctx, svc, sp.ID, "repo:docs/b.md"); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("archiving twice: %v", err)
	}
	if err := e.docs.SyncArchive(e.ctx, svc, sp.ID, "other:x.md"); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("archiving the entry of another account: %v", err)
	}

	// The source comes back: the entry is restored, with the new text.
	res, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, "repo:docs/b.md", "Text of b, back again.", "2"))
	if err != nil || res.Action != docs.SyncRestored || res.Document.Status != "approved" || !strings.Contains(res.Document.Content, "back again") {
		t.Errorf("restore: %+v %v", res, err)
	}
	if list, _ := e.docs.SyncList(e.ctx, svc, sp.ID, "repo:docs/"); len(list) != 2 {
		t.Errorf("after restoring: %+v", list)
	}
}

func TestSyncRulesOfTheSpaceApply(t *testing.T) {
	e := setup(t)
	open := e.team(t, "open", false, authz.None)
	approval := e.team(t, "approval", true, authz.None)
	svcOpen := e.serviceActor(t, "docs-sync", open.ID, authz.Author)
	svc := e.serviceActor(t, "approval-sync", approval.ID, authz.Author)
	foreign := e.team(t, "foreign", false, authz.None)

	// Without a role in the space the call finds nothing.
	if _, err := e.docs.Sync(e.ctx, svcOpen, syncParams(foreign.ID, "a.md", "Text", "1")); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("foreign space: %v", err)
	}
	if _, err := e.docs.SyncList(e.ctx, svcOpen, foreign.ID, ""); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("list in a foreign space: %v", err)
	}
	// An author in a space with approval writes drafts, and cannot replace an approved entry.
	res, err := e.docs.Sync(e.ctx, svc, syncParams(approval.ID, "a.md", "First text.", "1"))
	if err != nil || res.Document.Status != "draft" {
		t.Fatalf("draft in an approval space: %+v %v", res, err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE documents SET status = 'approved' WHERE id = $1`, res.Document.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.Sync(e.ctx, svc, syncParams(approval.ID, "a.md", "Second text.", "2")); !errors.Is(err, docs.ErrNeedsApproval) {
		t.Errorf("changing an approved entry as an author: %v", err)
	}
}

func TestSyncChecksItsInput(t *testing.T) {
	e := setup(t)
	sp := e.team(t, "code", false, authz.None)
	svc := e.serviceActor(t, "docs-sync", sp.ID, authz.Author)

	for name, key := range map[string]string{
		"empty":          "",
		"white space":    " a.md",
		"control":        "a\x00.md",
		"invisible":      "a‮.md",
		"too long":       strings.Repeat("k", docs.MaxSyncKeyLen+1),
		"trailing space": "a.md ",
	} {
		if _, err := e.docs.Sync(e.ctx, svc, syncParams(sp.ID, key, "Text", "1")); !errors.Is(err, ingest.ErrInvalid) {
			t.Errorf("key %s: %v", name, err)
		}
	}
	noHash := syncParams(sp.ID, "a.md", "Text", "1")
	noHash.Origin.SHA256 = ""
	if _, err := e.docs.Sync(e.ctx, svc, noHash); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("without a hash: %v", err)
	}
	badRef := syncParams(sp.ID, "a.md", "Text", "1")
	badRef.Origin.Ref = "not a reference"
	if _, err := e.docs.Sync(e.ctx, svc, badRef); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("with a bad reference: %v", err)
	}
	if err := docs.ValidSyncKey("git:repo:docs/a b.md"); err != nil {
		t.Errorf("a key with an inner space: %v", err)
	}
}
