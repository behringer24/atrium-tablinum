package search_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// fakeEmbedder maps keywords to axes: "alpha"/"alfa" -> x, "beta" -> y, otherwise z.
// "alfa" has no lexical overlap with "alpha", which stands in for cross-language matches.
type fakeEmbedder struct{ fail bool }

func (fakeEmbedder) Model() string              { return "fake" }
func (fakeEmbedder) Dimensions() int            { return dbtest.Dimensions }
func (fakeEmbedder) Ping(context.Context) error { return nil }
func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("embedder down")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		t = strings.ToLower(t)
		switch {
		case strings.Contains(t, "alpha"), strings.Contains(t, "alfa"):
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
	pool    *pgxpool.Pool
	users   *users.Store
	spaces  *spaces.Store
	checker *authz.Checker
	docs    *docs.Store
	search  *search.Service
}

func setup(t *testing.T, fail bool) env {
	t.Helper()
	pool := dbtest.NewPool(t)
	e := env{
		ctx:     context.Background(),
		pool:    pool,
		users:   users.New(pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true}),
		spaces:  spaces.New(pool),
		checker: authz.NewChecker(pool, time.Minute),
	}
	e.users.OnChange = e.checker.InvalidateAll
	e.spaces.OnChange = e.checker.InvalidateAll
	langs := []string{"german", "english", "simple"}
	ing, err := ingest.New(ingest.Config{MaxDocumentBytes: 5000, Chunk: chunk.Options{TargetTokens: 100, MaxTokens: 150}, FTSLanguages: langs}, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	e.docs = docs.New(pool, e.spaces, ing, docs.Options{PersonalMaxDocuments: 1000, PersonalMaxBytes: 1 << 30, DuplicateThreshold: 0.9})
	e.search, err = search.New(pool, fakeEmbedder{fail: fail}, search.Config{
		FTSLanguages: langs, CandidatesPerMethod: 50, RRFK: 60, DefaultLimit: 5, MaxLimit: 20, HNSWEfSearch: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) actor(t *testing.T, email string) docs.Actor {
	t.Helper()
	u, err := e.users.Provision(e.ctx, users.Identity{IDP: "g", Subject: email, Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return e.refresh(t, docs.Actor{User: u})
}

func (e env) refresh(t *testing.T, a docs.Actor) docs.Actor {
	t.Helper()
	u, err := e.users.Get(e.ctx, a.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.checker.EffectiveRoles(e.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	return docs.Actor{User: u, Roles: r, Client: "claude"}
}

func (e env) team(t *testing.T, slug string, approval bool, def authz.Role, members map[*docs.Actor]authz.Role) spaces.Space {
	t.Helper()
	sp, err := e.spaces.CreateTeam(e.ctx, spaces.CreateParams{Slug: slug, Name: strings.ToUpper(slug), RequireApproval: approval, DefaultRole: def})
	if err != nil {
		t.Fatal(err)
	}
	for a, role := range members {
		if err := e.spaces.SetMember(e.ctx, sp.ID, spaces.PrincipalUser, a.User.ID, role, a.User.ID); err != nil {
			t.Fatal(err)
		}
		*a = e.refresh(t, *a)
	}
	return sp
}

func (e env) put(t *testing.T, a docs.Actor, space uuid.UUID, title, content string, tags ...string) docs.Document {
	t.Helper()
	res, err := e.docs.Store(e.ctx, a, docs.StoreParams{SpaceID: space, Title: title, Content: content, Tags: tags, Force: true})
	if err != nil || res.Document == nil {
		t.Fatalf("Store %q: %v", title, err)
	}
	return *res.Document
}

func (e env) find(t *testing.T, a docs.Actor, q string, p ...func(*search.Params)) []search.Result {
	t.Helper()
	params := search.Params{Query: q}
	for _, f := range p {
		f(&params)
	}
	caller := search.Caller{Scope: a.Scope(), PersonalSpace: a.User.PersonalSpaceID, Client: "claude"}
	res, err := e.search.Search(e.ctx, caller, params)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func titles(rs []search.Result) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Title)
	}
	return out
}

func has(rs []search.Result, title string) bool {
	for _, r := range rs {
		if r.Title == title {
			return true
		}
	}
	return false
}

func TestHybridFindsByMeaningAndByExactToken(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	e.put(t, anna, *anna.User.PersonalSpaceID, "Englischer Eintrag", "alpha rollout checklist")
	e.put(t, anna, *anna.User.PersonalSpaceID, "Fehlercodes", "Der Fehler ERR-4711 tritt beim Export auf")
	e.put(t, anna, *anna.User.PersonalSpaceID, "Unrelated", "nothing relevant here")

	// "alfa" shares no word with "alpha": only the vector can match
	if rs := e.find(t, anna, "alfa Anleitung"); !has(rs, "Englischer Eintrag") {
		t.Errorf("vector hit missing: %v", titles(rs))
	}
	// exact token: only full text (simple) can match
	rs := e.find(t, anna, "ERR-4711")
	if len(rs) == 0 || rs[0].Title != "Fehlercodes" {
		t.Errorf("full-text hit missing: %v", titles(rs))
	}
}

func TestResultMetadata(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	sp := e.team(t, "doku", false, authz.None, map[*docs.Actor]authz.Role{&anna: authz.Author})
	e.put(t, anna, sp.ID, "Meta", "# Kopf\nalpha inhalt")
	rs := e.find(t, anna, "alpha")
	if len(rs) != 1 {
		t.Fatalf("%v", titles(rs))
	}
	r := rs[0]
	if r.SpaceName != "DOKU" || r.SpaceType != "team" || r.Status != "approved" || r.AuthorEmail != "anna@x.de" || r.Score <= 0 || r.HeadingPath != "Kopf" {
		t.Errorf("%+v", r)
	}
}

// Concept 17.3: support finds nothing from "Geschäftsführung" (management).
func TestUnrelatedSpaceIsInvisible(t *testing.T) {
	e := setup(t, false)
	gf := e.actor(t, "gf@x.de")
	sup := e.actor(t, "sup@x.de")
	gfSpace := e.team(t, "gf", false, authz.None, map[*docs.Actor]authz.Role{&gf: authz.Author})
	e.team(t, "support", false, authz.None, map[*docs.Actor]authz.Role{&sup: authz.Author})
	e.put(t, gf, gfSpace.ID, "Gehaltsbänder", "alpha vertraulich ERR-4711")

	if rs := e.find(t, sup, "alpha"); len(rs) != 0 {
		t.Errorf("vector leak: %v", titles(rs))
	}
	if rs := e.find(t, sup, "Gehaltsbänder ERR-4711"); len(rs) != 0 {
		t.Errorf("full-text leak: %v", titles(rs))
	}
	// a filter on an invisible space yields nothing, without an error too
	rs := e.find(t, sup, "alpha", func(p *search.Params) { p.Spaces = []uuid.UUID{gfSpace.ID} })
	if len(rs) != 0 {
		t.Errorf("space filter leak: %v", titles(rs))
	}
	if rs := e.find(t, gf, "alpha"); len(rs) != 1 {
		t.Errorf("authorized user finds nothing: %v", titles(rs))
	}
}

func TestPersonalSpacesStayPrivate(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	boss := e.actor(t, "boss@x.de") // instance admin
	e.put(t, anna, *anna.User.PersonalSpaceID, "Privat", "alpha ERR-4711")
	if rs := e.find(t, boss, "alpha ERR-4711"); len(rs) != 0 {
		t.Errorf("instance admin finds personal entry: %v", titles(rs))
	}
	if rs := e.find(t, anna, "ERR-4711"); len(rs) != 1 {
		t.Errorf("owner does not find own entry")
	}
}

func TestDraftsArchivedAndHandoffs(t *testing.T) {
	e := setup(t, false)
	author := e.actor(t, "author@x.de")
	cur := e.actor(t, "cur@x.de")
	reader := e.actor(t, "reader@x.de")
	sp := e.team(t, "wiki", true, authz.None, map[*docs.Actor]authz.Role{&author: authz.Author, &cur: authz.Curator, &reader: authz.Reader})

	e.put(t, author, sp.ID, "Entwurf", "alpha entwurf")
	approved := e.put(t, cur, sp.ID, "Freigegeben", "alpha frei")
	gone := e.put(t, cur, sp.ID, "Archiviert", "alpha weg")
	if err := e.docs.Archive(e.ctx, cur, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.SaveHandoff(e.ctx, author, "Handoff", "alpha handoff", 0); err != nil {
		t.Fatal(err)
	}

	if rs := e.find(t, reader, "alpha"); len(rs) != 1 || rs[0].DocumentID != approved.ID {
		t.Errorf("reader: %v", titles(rs))
	}
	rs := e.find(t, author, "alpha") // own draft + approved; no handoff, no archive
	if !has(rs, "Entwurf") || !has(rs, "Freigegeben") || len(rs) != 2 {
		t.Errorf("author: %v", titles(rs))
	}
	for _, r := range rs {
		if r.Title == "Entwurf" && r.Status != "draft" {
			t.Errorf("draft not marked: %s", r.Status)
		}
	}
	if rs := e.find(t, cur, "alpha"); len(rs) != 2 || !has(rs, "Entwurf") {
		t.Errorf("curator: %v", titles(rs))
	}
}

func TestExpiredHandoffNeverSearched(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	if _, err := e.docs.SaveHandoff(e.ctx, anna, "H", "alpha", 1); err != nil {
		t.Fatal(err)
	}
	if rs := e.find(t, anna, "alpha"); len(rs) != 0 {
		t.Errorf("handoff in search: %v", titles(rs))
	}
}

func TestTagAndSpaceFilters(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	a := e.team(t, "aaa", false, authz.None, map[*docs.Actor]authz.Role{&anna: authz.Author})
	b := e.team(t, "bbb", false, authz.None, map[*docs.Actor]authz.Role{&anna: authz.Author})
	e.put(t, anna, a.ID, "In A", "alpha", "Wichtig")
	e.put(t, anna, b.ID, "In B", "alpha", "anders")

	if rs := e.find(t, anna, "alpha"); len(rs) != 2 {
		t.Fatalf("without filter: %v", titles(rs))
	}
	if rs := e.find(t, anna, "alpha", func(p *search.Params) { p.Tags = []string{"wichtig"} }); len(rs) != 1 || rs[0].Title != "In A" {
		t.Errorf("tag filter: %v", titles(rs))
	}
	if rs := e.find(t, anna, "alpha", func(p *search.Params) { p.Spaces = []uuid.UUID{b.ID} }); len(rs) != 1 || rs[0].Title != "In B" {
		t.Errorf("space filter: %v", titles(rs))
	}
}

// Many invisible, closer documents must not empty the hit list.
func TestFilteredSearchStillFindsEnoughHits(t *testing.T) {
	e := setup(t, false)
	boss := e.actor(t, "boss@x.de")
	anna := e.actor(t, "anna@x.de")
	hidden := e.team(t, "hidden", false, authz.None, map[*docs.Actor]authz.Role{&boss: authz.Author})
	shown := e.team(t, "shown", false, authz.None, map[*docs.Actor]authz.Role{&anna: authz.Author})
	for i := 0; i < 70; i++ { // more than candidates_per_method
		e.put(t, boss, hidden.ID, fmt.Sprintf("Versteckt %d", i), "alpha versteckt")
	}
	for i := 0; i < 3; i++ {
		e.put(t, anna, shown.ID, fmt.Sprintf("Sichtbar %d", i), "alpha sichtbar")
	}
	if rs := e.find(t, anna, "alpha"); len(rs) != 3 {
		t.Errorf("hits = %d, want 3: %v", len(rs), titles(rs))
	}
}

func TestLimit(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	for i := 0; i < 8; i++ {
		e.put(t, anna, *anna.User.PersonalSpaceID, fmt.Sprintf("E%d", i), "alpha")
	}
	if n := len(e.find(t, anna, "alpha")); n != 5 {
		t.Errorf("default limit: %d", n)
	}
	if n := len(e.find(t, anna, "alpha", func(p *search.Params) { p.Limit = 2 })); n != 2 {
		t.Errorf("limit 2: %d", n)
	}
	if n := len(e.find(t, anna, "alpha", func(p *search.Params) { p.Limit = 999 })); n != 8 {
		t.Errorf("maximum: %d", n)
	}
}

func TestFallsBackToFullTextWhenEmbedderIsDown(t *testing.T) {
	e := setup(t, true)
	anna := e.actor(t, "anna@x.de")
	e.put(t, anna, *anna.User.PersonalSpaceID, "Fehler", "ERR-4711 beim Export")
	rs := e.find(t, anna, "ERR-4711")
	if len(rs) != 1 {
		t.Fatalf("fallback: %v", titles(rs))
	}
}

func TestInvalidQueries(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	caller := search.Caller{Scope: anna.Scope()}
	for _, q := range []string{"", "   ", strings.Repeat("x", 501)} {
		if _, err := e.search.Search(e.ctx, caller, search.Params{Query: q}); !errors.Is(err, search.ErrInvalid) {
			t.Errorf("%.10q: %v", q, err)
		}
	}
	// stop words only yield an empty tsquery, but no error
	if _, err := e.search.Search(e.ctx, caller, search.Params{Query: "und der die"}); err != nil {
		t.Errorf("stop words: %v", err)
	}
	// syntax that would break in to_tsquery is harmless with websearch_to_tsquery
	if _, err := e.search.Search(e.ctx, caller, search.Params{Query: `"offen & | ! ( :* <->`}); err != nil {
		t.Errorf("special characters: %v", err)
	}
}

func TestSearchAudit(t *testing.T) {
	e := setup(t, false)
	anna := e.actor(t, "anna@x.de")
	sp := e.team(t, "doku", false, authz.None, map[*docs.Actor]authz.Role{&anna: authz.Author})
	e.put(t, anna, sp.ID, "X", "alpha")

	count := func(sql string, args ...any) int {
		var n int
		if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// search across all spaces (incl. personal): only the hit count, no search text
	e.find(t, anna, "geheimer suchtext")
	if n := count(`SELECT count(*) FROM audit_log WHERE action='search' AND details ? 'hits' AND NOT details ? 'query'`); n != 1 {
		t.Errorf("audit with personal space: %d", n)
	}
	// search in the team space only: the search text may be logged
	e.find(t, anna, "teamsuche", func(p *search.Params) { p.Spaces = []uuid.UUID{sp.ID} })
	if n := count(`SELECT count(*) FROM audit_log WHERE action='search' AND details->>'query' = 'teamsuche'`); n != 1 {
		t.Errorf("audit team search: %d", n)
	}
	if n := count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%geheimer suchtext%'`); n != 0 {
		t.Errorf("search text from personal scope in audit")
	}
}

// The archive is searched only on explicit request and then on its own: archived entries
// never mix into normal hits, and only those who may restore an entry find it.
func TestArchiveSearch(t *testing.T) {
	e := setup(t, false)
	author := e.actor(t, "author@x.de")
	cur := e.actor(t, "cur@x.de")
	reader := e.actor(t, "reader@x.de")
	sp := e.team(t, "wiki", false, authz.None, map[*docs.Actor]authz.Role{&author: authz.Author, &cur: authz.Curator, &reader: authz.Reader})

	e.put(t, cur, sp.ID, "Aktiv", "alpha aktuell")
	old := e.put(t, author, sp.ID, "Alt", "alpha veraltet")
	if err := e.docs.Archive(e.ctx, author, old.ID); err != nil {
		t.Fatal(err)
	}
	onlyArchive := func(p *search.Params) { p.Archive = true }

	if rs := e.find(t, cur, "alpha"); len(rs) != 1 || rs[0].Title != "Aktiv" {
		t.Errorf("normal search: %v", titles(rs))
	}
	for _, a := range []docs.Actor{author, cur} {
		rs := e.find(t, a, "alpha", onlyArchive)
		if len(rs) != 1 || rs[0].Title != "Alt" || rs[0].Status != "archived" {
			t.Errorf("archive search: %v", titles(rs))
		}
	}
	if rs := e.find(t, reader, "alpha", onlyArchive); len(rs) != 0 {
		t.Errorf("reader finds archive: %v", titles(rs))
	}
	// full text (exact word) works in the archive just like for active entries
	if rs := e.find(t, cur, "veraltet", onlyArchive); len(rs) != 1 {
		t.Errorf("archive full text: %v", titles(rs))
	}
	// restored: findable normally again, no longer in the archive
	if _, err := e.docs.Unarchive(e.ctx, author, old.ID); err != nil {
		t.Fatal(err)
	}
	if rs := e.find(t, reader, "alpha"); len(rs) != 2 {
		t.Errorf("after restore: %v", titles(rs))
	}
	if rs := e.find(t, cur, "alpha", onlyArchive); len(rs) != 0 {
		t.Errorf("archive after restore: %v", titles(rs))
	}
}
