package docs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/guard"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// fakeEmbedder maps keywords to orthogonal unit vectors so similarity is predictable.
type fakeEmbedder struct{}

func (fakeEmbedder) Model() string              { return "fake" }
func (fakeEmbedder) Dimensions() int            { return dbtest.Dimensions }
func (fakeEmbedder) Ping(context.Context) error { return nil }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		t = strings.ToLower(t)
		switch {
		case strings.Contains(t, "alpha"):
			out[i] = []float32{1, 0, 0, 0}
		case strings.Contains(t, "beta"):
			out[i] = []float32{0, 1, 0, 0}
		default:
			out[i] = []float32{0, 0, 1, 0}
		}
	}
	return out, nil
}

type env struct {
	ctx     context.Context
	users   *users.Store
	spaces  *spaces.Store
	checker *authz.Checker
	docs    *docs.Store
	pool    *pgxpool.Pool
}

func queryInt(t *testing.T, e env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.NewPool(t)
	e := env{
		pool:    pool,
		ctx:     context.Background(),
		users:   users.New(pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true}),
		spaces:  spaces.New(pool),
		checker: authz.NewChecker(pool, time.Minute),
	}
	e.users.OnChange = e.checker.InvalidateAll
	e.spaces.OnChange = e.checker.InvalidateAll
	ing, err := ingest.New(ingest.Config{
		MaxDocumentBytes: 2000,
		Chunk:            chunk.Options{TargetTokens: 50, MaxTokens: 80},
		FTSLanguages:     []string{"german", "english", "simple"},
	}, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	e.docs = docs.New(pool, e.spaces, ing, docs.Options{
		PersonalMaxDocuments: 3, PersonalMaxBytes: 5000, HandoffTTLDays: 14, DuplicateThreshold: 0.9,
	})
	return e
}

func (e env) actor(t *testing.T, email string) docs.Actor {
	t.Helper()
	u, err := e.users.Provision(e.ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: email, Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return e.actorFor(t, u)
}

func (e env) actorFor(t *testing.T, u users.User) docs.Actor {
	t.Helper()
	r, err := e.checker.EffectiveRoles(e.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	return docs.Actor{User: u, Roles: r, Client: "claude"}
}

// refresh recomputes an actor after membership changes.
func (e env) refresh(t *testing.T, a docs.Actor) docs.Actor {
	t.Helper()
	u, err := e.users.Get(e.ctx, a.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e.actorFor(t, u)
}

func (e env) team(t *testing.T, slug string, approval bool, def authz.Role) spaces.Space {
	t.Helper()
	sp, err := e.spaces.CreateTeam(e.ctx, spaces.CreateParams{Slug: slug, Name: slug, RequireApproval: approval, DefaultRole: def})
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func (e env) member(t *testing.T, sp spaces.Space, a docs.Actor, role authz.Role) {
	t.Helper()
	if err := e.spaces.SetMember(e.ctx, sp.ID, spaces.PrincipalUser, a.User.ID, role, a.User.ID); err != nil {
		t.Fatal(err)
	}
}

func personal(a docs.Actor) uuid.UUID { return *a.User.PersonalSpaceID }

func mustStore(t *testing.T, e env, a docs.Actor, space uuid.UUID, title, content string) docs.Document {
	t.Helper()
	res, err := e.docs.Store(e.ctx, a, docs.StoreParams{SpaceID: space, Title: title, Content: content, Force: true})
	if err != nil || res.Document == nil {
		t.Fatalf("Store: %v %+v", err, res)
	}
	return *res.Document
}

func TestStoreInPersonalSpace(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	res, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: " Titel ", Content: "Inhalt alpha", Tags: []string{"B", "a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Document
	if d.Status != "approved" || d.Title != "Titel" || d.Version != 1 || d.Source != "claude" || d.AuthorEmail != "anna@x.de" {
		t.Errorf("%+v", d)
	}
	if strings.Join(d.Tags, ",") != "a,b" {
		t.Errorf("Tags = %v", d.Tags)
	}
	got, err := e.docs.Get(e.ctx, anna, d.ID)
	if err != nil || got.Content != "Inhalt alpha" {
		t.Fatalf("Get: %v", err)
	}
}

func TestStoreRejectsSecretsAndTooLarge(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	_, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "t", Content: "password: Tr0ub4dor&3"})
	var ge *guard.Error
	if !errors.As(err, &ge) {
		t.Fatalf("err = %v", err)
	}
	_, err = e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "t", Content: strings.Repeat("x", 2001)})
	if !errors.Is(err, ingest.ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	_, err = e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: " ", Content: "x"})
	if !errors.Is(err, ingest.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	list, _ := e.docs.List(e.ctx, anna, docs.ListParams{SpaceID: personal(anna)})
	if len(list) != 0 {
		t.Errorf("rejected content was stored: %d", len(list))
	}
}

func TestRolesGateWriting(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	ben := e.actor(t, "ben@x.de")
	sp := e.team(t, "support", false, authz.None)
	hidden := e.team(t, "gf", false, authz.None)
	e.member(t, sp, anna, authz.Reader)
	anna = e.refresh(t, anna)

	_, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: sp.ID, Title: "t", Content: "c"})
	if !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("Reader: %v", err)
	}
	// invisible and nonexistent spaces cannot be told apart
	_, errHidden := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: hidden.ID, Title: "t", Content: "c"})
	_, errMissing := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: uuid.New(), Title: "t", Content: "c"})
	if !errors.Is(errHidden, docs.ErrNotFound) || !errors.Is(errMissing, docs.ErrNotFound) {
		t.Errorf("hidden=%v missing=%v", errHidden, errMissing)
	}
	// somebody else's personal space
	_, err = e.docs.Store(e.ctx, ben, docs.StoreParams{SpaceID: personal(anna), Title: "t", Content: "c"})
	if !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("someone else's personal space: %v", err)
	}
}

func TestApprovalWorkflowVisibility(t *testing.T) {
	e := setup(t)
	author := e.actor(t, "author@x.de")
	curator := e.actor(t, "cur@x.de")
	reader := e.actor(t, "reader@x.de")
	sp := e.team(t, "wiki", true, authz.None)
	e.member(t, sp, author, authz.Author)
	e.member(t, sp, curator, authz.Curator)
	e.member(t, sp, reader, authz.Reader)
	author, curator, reader = e.refresh(t, author), e.refresh(t, curator), e.refresh(t, reader)

	draft := mustStore(t, e, author, sp.ID, "Entwurf", "inhalt")
	if draft.Status != "draft" {
		t.Fatalf("author entry: %s", draft.Status)
	}
	if _, err := e.docs.Get(e.ctx, reader, draft.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("reader sees draft: %v", err)
	}
	if _, err := e.docs.Get(e.ctx, author, draft.ID); err != nil {
		t.Errorf("author does not see own draft: %v", err)
	}
	if _, err := e.docs.Get(e.ctx, curator, draft.ID); err != nil {
		t.Errorf("curator does not see draft: %v", err)
	}
	if l, _ := e.docs.List(e.ctx, reader, docs.ListParams{SpaceID: sp.ID}); len(l) != 0 {
		t.Errorf("reader list: %d", len(l))
	}
	if l, _ := e.docs.List(e.ctx, curator, docs.ListParams{SpaceID: sp.ID}); len(l) != 1 {
		t.Errorf("curator list: %d", len(l))
	}
	if cd := mustStore(t, e, curator, sp.ID, "Direkt", "inhalt"); cd.Status != "approved" {
		t.Errorf("curator entry: %s", cd.Status)
	}
	// an author may revise their own draft, but not approved entries
	title := "Entwurf v2"
	if d, err := e.docs.Update(e.ctx, author, docs.UpdateParams{ID: draft.ID, Title: &title}); err != nil || d.Status != "draft" {
		t.Errorf("revise draft: %v %+v", err, d)
	}
	approved := mustStore(t, e, curator, sp.ID, "Frei", "inhalt")
	if _, err := e.docs.Update(e.ctx, author, docs.UpdateParams{ID: approved.ID, Title: &title}); err == nil {
		t.Error("author changes approved entry of an approval space")
	}
}

// An admin asks for a draft in a space of another field; the curator of that space checks it.
func TestDraftOnRequestAndReview(t *testing.T) {
	e := setup(t)
	boss := e.actor(t, "boss@x.de") // instance admin, admin in every team space
	curator := e.actor(t, "cur@x.de")
	author := e.actor(t, "author@x.de")
	reader := e.actor(t, "reader@x.de")
	sp := e.team(t, "recht", false, authz.None) // no approval required
	e.member(t, sp, curator, authz.Curator)
	e.member(t, sp, author, authz.Author)
	e.member(t, sp, reader, authz.Reader)
	boss, curator, author, reader = e.refresh(t, boss), e.refresh(t, curator), e.refresh(t, author), e.refresh(t, reader)

	storeDraft := func(a docs.Actor, title string) docs.Document {
		t.Helper()
		res, err := e.docs.Store(e.ctx, a, docs.StoreParams{SpaceID: sp.ID, Title: title, Content: "inhalt", Force: true, Draft: true})
		if err != nil || res.Document == nil {
			t.Fatalf("store draft: %v %+v", err, res)
		}
		return *res.Document
	}

	// without the request the admin's entry is approved right away, with it the entry is a draft
	if d := mustStore(t, e, boss, sp.ID, "Direkt", "inhalt"); d.Status != "approved" {
		t.Errorf("admin without draft request: %s", d.Status)
	}
	draft := storeDraft(boss, "Vertragsmuster")
	if draft.Status != "draft" {
		t.Fatalf("admin with draft request: %s", draft.Status)
	}
	if _, err := e.docs.Store(e.ctx, boss, docs.StoreParams{SpaceID: personal(boss), Title: "t", Content: "c", Draft: true}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("draft in personal space: %v", err)
	}

	// visible only to the author and curators, also in the list with a status filter
	if _, err := e.docs.Get(e.ctx, reader, draft.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("reader sees draft: %v", err)
	}
	if l, err := e.docs.List(e.ctx, curator, docs.ListParams{SpaceID: sp.ID, Status: "draft"}); err != nil || len(l) != 1 || l[0].ID != draft.ID {
		t.Errorf("curator list draft: %v %+v", err, l)
	}
	if l, _ := e.docs.List(e.ctx, curator, docs.ListParams{SpaceID: sp.ID, Status: "approved"}); len(l) != 1 {
		t.Errorf("curator list approved: %d", len(l))
	}
	if l, _ := e.docs.List(e.ctx, reader, docs.ListParams{SpaceID: sp.ID, Status: "draft"}); len(l) != 0 {
		t.Errorf("reader list draft: %d", len(l))
	}
	if _, err := e.docs.List(e.ctx, curator, docs.ListParams{SpaceID: sp.ID, Status: "kaputt"}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("invalid status: %v", err)
	}

	// curators and admins may review; only admins may review their own draft
	curDraft := storeDraft(curator, "Vom Kurator")
	if _, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: curDraft.ID, Approve: true}); !errors.Is(err, docs.ErrOwnDraft) {
		t.Errorf("curator approves own draft: %v", err)
	}
	if d, err := e.docs.Review(e.ctx, boss, docs.ReviewParams{ID: curDraft.ID, Approve: true}); err != nil || d.Status != "approved" {
		t.Errorf("admin approves curator's draft: %v %+v", err, d)
	}
	if _, err := e.docs.Review(e.ctx, reader, docs.ReviewParams{ID: draft.ID, Approve: true}); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("reader reviews: %v", err)
	}
	authorsDraft := storeDraft(author, "Von Autor")
	if _, err := e.docs.Review(e.ctx, author, docs.ReviewParams{ID: authorsDraft.ID, Approve: true}); !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("author approves own draft: %v", err)
	}

	// sending back needs a note; the author sees it and revises
	if _, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: draft.ID}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("returning without note: %v", err)
	}
	rejected, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: draft.ID, Note: "  Clause 3 missing  "})
	if err != nil || rejected.Status != "rejected" || rejected.ReviewNote != "Clause 3 missing" || rejected.Version != 2 {
		t.Fatalf("returning: %v %+v", err, rejected)
	}
	if got, err := e.docs.Get(e.ctx, boss, draft.ID); err != nil || got.ReviewNote != "Clause 3 missing" {
		t.Errorf("author does not see the note: %v %+v", err, got)
	}
	if _, err := e.docs.Get(e.ctx, reader, draft.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("reader sees returned draft: %v", err)
	}
	if _, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: draft.ID, Approve: true}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("review returned entry: %v", err)
	}
	content := "inhalt mit Klausel 3"
	revised, err := e.docs.Update(e.ctx, boss, docs.UpdateParams{ID: draft.ID, Content: &content})
	if err != nil || revised.Status != "draft" || revised.ReviewNote != "" {
		t.Fatalf("revising: %v %+v", err, revised)
	}

	// approval: afterwards the reader sees the entry, version and log record it
	approved, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: draft.ID, Approve: true, Note: "passt"})
	if err != nil || approved.Status != "approved" || approved.ReviewNote != "passt" || approved.Version != 4 {
		t.Fatalf("approval: %v %+v", err, approved)
	}
	if _, err := e.docs.Get(e.ctx, reader, draft.ID); err != nil {
		t.Errorf("reader does not see approved entry: %v", err)
	}
	if _, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: draft.ID, Approve: true}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("review approved entry: %v", err)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM document_versions WHERE document_id = $1`, draft.ID); n != 4 {
		t.Errorf("versions: %d", n)
	}
	for action, want := range map[string]int{"reject": 1, "approve": 1} {
		if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = $1 AND document_id = $2`, action, draft.ID); n != want {
			t.Errorf("Audit %s: %d", action, n)
		}
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE document_id = $1 AND details ? 'self'`, draft.ID); n != 0 {
		t.Errorf("approval by someone else recorded as self-approval: %d", n)
	}

	// untying the knot: the admin approves their own draft themselves, the log notes it
	own := storeDraft(boss, "Eigene Freigabe")
	if d, err := e.docs.Review(e.ctx, boss, docs.ReviewParams{ID: own.ID, Approve: true}); err != nil || d.Status != "approved" {
		t.Fatalf("admin approves own draft: %v %+v", err, d)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'approve' AND document_id = $1 AND details ? 'self'`, own.ID); n != 1 {
		t.Errorf("self-approval not in audit log: %d", n)
	}
	if _, err := e.docs.Get(e.ctx, reader, own.ID); err != nil {
		t.Errorf("reader does not see self-approved entry: %v", err)
	}
}

func TestUpdateRightsAndVersions(t *testing.T) {
	e := setup(t)
	a1 := e.actor(t, "a1@x.de")
	a2 := e.actor(t, "a2@x.de")
	cur := e.actor(t, "cur@x.de")
	sp := e.team(t, "doku", false, authz.None)
	e.member(t, sp, a1, authz.Author)
	e.member(t, sp, a2, authz.Author)
	e.member(t, sp, cur, authz.Curator)
	a1, a2, cur = e.refresh(t, a1), e.refresh(t, a2), e.refresh(t, cur)

	d := mustStore(t, e, a1, sp.ID, "Doc", "kurz alpha")
	newContent := "neu\n\n" + strings.Repeat("beta wort ", 60)
	title := "Doc 2"
	if _, err := e.docs.Update(e.ctx, a2, docs.UpdateParams{ID: d.ID, Content: &newContent}); !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("other author: %v", err)
	}
	up, err := e.docs.Update(e.ctx, a1, docs.UpdateParams{ID: d.ID, Content: &newContent, Title: &title})
	if err != nil || up.Version != 2 || up.Title != "Doc 2" {
		t.Fatalf("Update: %v %+v", err, up)
	}
	if up, err = e.docs.Update(e.ctx, cur, docs.UpdateParams{ID: d.ID, Tags: &[]string{"x"}}); err != nil || up.Version != 3 || up.Content != strings.TrimSpace(newContent) {
		t.Fatalf("curator update: %v %+v", err, up)
	}
	// archiving: the entry disappears
	if err := e.docs.Archive(e.ctx, a2, d.ID); !errors.Is(err, docs.ErrForbidden) {
		t.Errorf("other author archives: %v", err)
	}
	if err := e.docs.Archive(e.ctx, a1, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.Get(e.ctx, a2, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("archived entry readable by outsiders: %v", err)
	}
	if got, err := e.docs.Get(e.ctx, a1, d.ID); err != nil || got.Status != "archived" {
		t.Errorf("author should see the archived entry in order to restore it: %v %s", err, got.Status)
	}
}

func TestPersonalSpaceInvisibleToOthers(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	boss := e.actor(t, "boss@x.de")
	d := mustStore(t, e, anna, personal(anna), "Privat", "geheim alpha")
	if _, err := e.docs.Get(e.ctx, boss, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("instance admin reads personal entry: %v", err)
	}
	if _, err := e.docs.List(e.ctx, boss, docs.ListParams{SpaceID: personal(anna)}); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("instance admin lists personal space: %v", err)
	}
	if err := e.docs.Archive(e.ctx, boss, d.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("instance admin archives: %v", err)
	}
}

func TestDuplicateCheck(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	ben := e.actor(t, "ben@x.de")
	sp := e.team(t, "wiki", false, authz.None)
	e.member(t, sp, anna, authz.Author)
	anna = e.refresh(t, anna)
	first := mustStore(t, e, anna, sp.ID, "Erstes", "alpha text")

	res, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: sp.ID, Title: "Zweites", Content: "nochmal alpha"})
	if err != nil || res.Document != nil || len(res.Duplicates) != 1 || res.Duplicates[0].ID != first.ID {
		t.Fatalf("duplicate not reported: %v %+v", err, res)
	}
	if res, err = e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: sp.ID, Title: "Anders", Content: "beta text"}); err != nil || res.Document == nil {
		t.Errorf("dissimilar entry: %v %+v", err, res)
	}
	if res, err = e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: sp.ID, Title: "Erzwungen", Content: "alpha", Force: true}); err != nil || res.Document == nil {
		t.Errorf("Force: %v", err)
	}
	// similar entry in somebody else's space: no duplicate hint (different space)
	if res, err = e.docs.Store(e.ctx, ben, docs.StoreParams{SpaceID: personal(ben), Title: "Eigener", Content: "alpha privat"}); err != nil || res.Document == nil {
		t.Errorf("duplicate hint from someone else's space: %v %+v", err, res)
	}
}

func TestPersonalQuota(t *testing.T) {
	e := setup(t) // 3 entries, 5000 bytes
	anna := e.actor(t, "anna@x.de")
	for i := 0; i < 3; i++ {
		mustStore(t, e, anna, personal(anna), "T", "inhalt")
	}
	_, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "T", Content: "vierter", Force: true})
	if !errors.Is(err, docs.ErrQuota) {
		t.Fatalf("entry limit: %v", err)
	}
	// per-user quota overrides the default
	more := 10
	if err := e.users.SetQuota(e.ctx, anna.User.ID, &more, nil); err != nil {
		t.Fatal(err)
	}
	anna = e.refresh(t, anna)
	mustStore(t, e, anna, personal(anna), "T", "vierter")
	big := strings.Repeat("y", 2000)
	mustStore(t, e, anna, personal(anna), "T", big)
	mustStore(t, e, anna, personal(anna), "T", big)
	if _, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "T", Content: big, Force: true}); !errors.Is(err, docs.ErrQuota) {
		t.Fatalf("byte limit: %v", err)
	}
}

func TestHandoffs(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	ben := e.actor(t, "ben@x.de")
	h1, err := e.docs.SaveHandoff(e.ctx, anna, "Status 1", "Goal and next steps", 0)
	if err != nil || h1.Kind != "handoff" || h1.ExpiresAt == nil || h1.ExpiresAt.Before(time.Now().Add(13*24*time.Hour)) {
		t.Fatalf("%v %+v", err, h1)
	}
	h2, _ := e.docs.SaveHandoff(e.ctx, anna, "Stand 2", "weiter", 1)

	list, err := e.docs.ListHandoffs(e.ctx, anna, 0)
	if err != nil || len(list) != 2 || list[0].ID != h2.ID || list[0].Content != "" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if got, err := e.docs.LoadHandoff(e.ctx, anna, nil); err != nil || got.ID != h2.ID || got.Content != "weiter" {
		t.Errorf("latest: %v %+v", err, got)
	}
	if got, err := e.docs.LoadHandoff(e.ctx, anna, &h1.ID); err != nil || got.ID != h1.ID {
		t.Errorf("by ID: %v", err)
	}
	if _, err := e.docs.LoadHandoff(e.ctx, ben, &h1.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("someone else's handoff: %v", err)
	}
	if _, err := e.docs.LoadHandoff(e.ctx, ben, nil); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("no own handoff: %v", err)
	}
	// handoffs do not show up in the knowledge list
	if l, _ := e.docs.List(e.ctx, anna, docs.ListParams{SpaceID: personal(anna)}); len(l) != 0 {
		t.Errorf("handoffs in knowledge list: %d", len(l))
	}
	// no chunks => not searchable
	if n := countChunks(t, e, h1.ID); n != 0 {
		t.Errorf("handoff has %d chunks", n)
	}
	// update_knowledge does not change handoffs
	title := "x"
	if _, err := e.docs.Update(e.ctx, anna, docs.UpdateParams{ID: h1.ID, Title: &title}); !errors.Is(err, ingest.ErrInvalid) {
		t.Errorf("update of a handoff: %v", err)
	}
}

func countChunks(t *testing.T, e env, doc uuid.UUID) int {
	t.Helper()
	return queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1`, doc)
}

func TestChunksAndAudit(t *testing.T) {
	e := setup(t)
	anna := e.actor(t, "anna@x.de")
	sp := e.team(t, "doku", false, authz.None)
	e.member(t, sp, anna, authz.Author)
	anna = e.refresh(t, anna)

	long := "# Abschnitt\n" + strings.Repeat("alpha satz. ", 40) + "\n\n## Zwei\n" + strings.Repeat("beta satz. ", 40)
	d := mustStore(t, e, anna, sp.ID, "Lang", long)
	if n := countChunks(t, e, d.ID); n < 2 {
		t.Errorf("Chunks = %d", n)
	}
	short := "kurz"
	if _, err := e.docs.Update(e.ctx, anna, docs.UpdateParams{ID: d.ID, Content: &short}); err != nil {
		t.Fatal(err)
	}
	if n := countChunks(t, e, d.ID); n != 1 {
		t.Errorf("old chunks not replaced: %d", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM document_versions WHERE document_id = $1`, d.ID); n != 2 {
		t.Errorf("versions = %d", n)
	}
	// tsv covers all languages: "satz" (german) is also found as a stem
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND tsv @@ to_tsquery('simple','kurz')`, d.ID); n != 1 {
		t.Errorf("tsv hits = %d", n)
	}
	// audit: team entry with details, personal entry without
	pd := mustStore(t, e, anna, personal(anna), "Privat", "x")
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE document_id = $1 AND details IS NOT NULL`, d.ID); n < 1 {
		t.Errorf("team audit without details: %d", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE document_id = $1`, pd.ID); n != 1 {
		t.Errorf("personal audit = %d", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE document_id = $1 AND details IS NOT NULL`, pd.ID); n != 0 {
		t.Errorf("personal audit contains details")
	}
}

// Archived entries leave the active knowledge base for everybody. Only those who may
// archive an entry (its author, the curators of the space) list, read and restore it.
func TestArchiveListAndRestore(t *testing.T) {
	e := setup(t)
	author := e.actor(t, "author@x.de")
	other := e.actor(t, "other@x.de")
	curator := e.actor(t, "cur@x.de")
	reader := e.actor(t, "reader@x.de")
	sp := e.team(t, "wiki", true, authz.None)
	e.member(t, sp, author, authz.Author)
	e.member(t, sp, other, authz.Author)
	e.member(t, sp, curator, authz.Curator)
	e.member(t, sp, reader, authz.Reader)
	author, other, curator, reader = e.refresh(t, author), e.refresh(t, other), e.refresh(t, curator), e.refresh(t, reader)

	byCurator := mustStore(t, e, curator, sp.ID, "Vom Kurator", "inhalt")
	byAuthor := mustStore(t, e, author, sp.ID, "Vom Autor", "inhalt") // approval space: draft
	if _, err := e.docs.Review(e.ctx, curator, docs.ReviewParams{ID: byAuthor.ID, Approve: true}); err != nil {
		t.Fatal(err)
	}
	asDraft := mustStore(t, e, author, sp.ID, "Noch Entwurf", "inhalt")
	for _, c := range []struct {
		a  docs.Actor
		id uuid.UUID
	}{{curator, byCurator.ID}, {author, byAuthor.ID}, {author, asDraft.ID}} {
		if err := e.docs.Archive(e.ctx, c.a, c.id); err != nil {
			t.Fatal(err)
		}
	}

	archived := func(a docs.Actor) int {
		l, err := e.docs.List(e.ctx, a, docs.ListParams{SpaceID: sp.ID, Status: "archived"})
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks`); n == 0 {
		t.Fatal("archiving must not delete the search index, otherwise archive search is impossible")
	}
	// normal lists: nothing archived
	for _, a := range []docs.Actor{author, curator, reader} {
		if l, _ := e.docs.List(e.ctx, a, docs.ListParams{SpaceID: sp.ID}); len(l) != 0 {
			t.Errorf("active list shows %d archived entries", len(l))
		}
	}
	// archive list: curator sees all, author only their own, others nothing
	if n := archived(curator); n != 3 {
		t.Errorf("curator's archive: %d", n)
	}
	if n := archived(author); n != 2 {
		t.Errorf("author's archive: %d", n)
	}
	if n := archived(other); n != 0 {
		t.Errorf("archive of another author: %d", n)
	}
	if n := archived(reader); n != 0 {
		t.Errorf("reader's archive: %d", n)
	}
	// loading by ID follows the same rule
	if d, err := e.docs.Get(e.ctx, curator, byAuthor.ID); err != nil || d.Status != "archived" {
		t.Errorf("curator loads archived entry: %v %s", err, d.Status)
	}
	for _, a := range []docs.Actor{other, reader} {
		if _, err := e.docs.Get(e.ctx, a, byAuthor.ID); !errors.Is(err, docs.ErrNotFound) {
			t.Errorf("Get by unauthorized users: %v", err)
		}
	}
	if _, err := e.docs.Get(e.ctx, author, byCurator.ID); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("author sees other author's archived entry: %v", err)
	}
	// archived entries cannot be changed, only restored
	title := "x"
	if _, err := e.docs.Update(e.ctx, curator, docs.UpdateParams{ID: byCurator.ID, Title: &title}); !errors.Is(err, docs.ErrNotFound) {
		t.Errorf("update in archive: %v", err)
	}

	// restoring: whoever may not see an entry cannot restore it either; authors not other people's entries
	for _, c := range []struct {
		a  docs.Actor
		id uuid.UUID
	}{{reader, byAuthor.ID}, {other, byAuthor.ID}, {author, byCurator.ID}} {
		if _, err := e.docs.Unarchive(e.ctx, c.a, c.id); !errors.Is(err, docs.ErrNotFound) {
			t.Errorf("Unarchive by unauthorized users: %v", err)
		}
	}
	// curator: approved stays approved
	if d, err := e.docs.Unarchive(e.ctx, curator, byCurator.ID); err != nil || d.Status != "approved" {
		t.Errorf("curator entry restored: %v %s", err, d.Status)
	}
	// author in an approval space: their previously approved entry goes back to review
	if d, err := e.docs.Unarchive(e.ctx, author, byAuthor.ID); err != nil || d.Status != "draft" {
		t.Errorf("author entry restored: %v %s", err, d.Status)
	}
	// a draft stays a draft
	if d, err := e.docs.Unarchive(e.ctx, curator, asDraft.ID); err != nil || d.Status != "draft" {
		t.Errorf("draft restored: %v %s", err, d.Status)
	}
	// not archived -> nothing to do
	if _, err := e.docs.Unarchive(e.ctx, curator, byCurator.ID); err == nil {
		t.Error("Unarchive of an active entry")
	}
	if l, _ := e.docs.List(e.ctx, reader, docs.ListParams{SpaceID: sp.ID}); len(l) != 1 {
		t.Errorf("reader sees %d entries after restore, want 1 (the approved one)", len(l))
	}
	if n := archived(curator); n != 0 {
		t.Errorf("archive after restore: %d", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM audit_log WHERE action = 'unarchive'`); n != 3 {
		t.Errorf("audit entries unarchive: %d", n)
	}
}
