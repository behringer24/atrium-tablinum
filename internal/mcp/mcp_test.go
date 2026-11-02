package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func TestMatchSpace(t *testing.T) {
	list := []spaces.Space{
		{ID: uuid.New(), Slug: "personal-1", Name: "Anna", Type: "personal"},
		{ID: uuid.New(), Slug: "support", Name: "Kundensupport", Type: "team"},
		{ID: uuid.New(), Slug: "support-intern", Name: "Support Intern", Type: "team"},
		{ID: uuid.New(), Slug: "vertrieb", Name: "Vertrieb", Type: "team"},
	}
	cases := []struct {
		ref   string
		want  string // slug of the single match
		cands int
	}{
		{"support", "support", 0},               // Kürzel exakt schlägt Teiltreffer
		{"Vertrieb", "vertrieb", 0},             // Name, Groß-/Kleinschreibung egal
		{"kunden", "support", 0},                // eindeutiges Fragment
		{"Support Intern", "support-intern", 0}, // Name exakt
		{"supp", "", 2},                         // mehrdeutig
		{"Mein persönlicher Bereich", "personal-1", 0},
		{"privat", "personal-1", 0},
		{"gibtsnicht", "", 0},
		{"anna", "", 0}, // persönliche Bereiche nur über Alias
		{"  ", "", 0},
	}
	for _, tc := range cases {
		sp, cands := matchSpace(list, tc.ref)
		got := ""
		if sp != nil {
			got = sp.Slug
		}
		if got != tc.want || len(cands) != tc.cands {
			t.Errorf("%q: got %q/%d, want %q/%d", tc.ref, got, len(cands), tc.want, tc.cands)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, 50*time.Millisecond)
	id := uuid.New()
	if !l.allow(id) || !l.allow(id) || l.allow(id) {
		t.Fatal("Limit von 2 nicht eingehalten")
	}
	if !l.allow(uuid.New()) {
		t.Error("anderer Nutzer betroffen")
	}
	time.Sleep(60 * time.Millisecond)
	if !l.allow(id) {
		t.Error("Fenster läuft nicht ab")
	}
	if !newLimiter(0, time.Minute).allow(id) {
		t.Error("Limit 0 = unbegrenzt")
	}
}

func TestDescriptionsRenderAndOverride(t *testing.T) {
	s := New(Deps{}, Settings{ProductName: "Atrium Tablinum", ShortName: "Atrium", OrganizationName: "Example Corp", HandoffTTLDays: 14,
		DescriptionOverrides: map[string]string{"list_spaces": "Eigener Text für {{.ProductName}}", "get_knowledge": "{{.Kaputt"}})
	if s.desc["list_spaces"] != "Eigener Text für Atrium Tablinum" {
		t.Errorf("Override: %q", s.desc["list_spaces"])
	}
	if !strings.Contains(s.desc["get_knowledge"], "„Atrium Tablinum“") {
		t.Errorf("Fallback bei ungültigem Template: %q", s.desc["get_knowledge"])
	}
	// Nutzer sagen meist kurz „Atrium“: Langform, Kurzform und Hinweis müssen beim Modell ankommen
	if !strings.Contains(s.desc["store_knowledge"], "Speichere das in Atrium") || !strings.Contains(s.desc["search_knowledge"], "„Atrium“") {
		t.Errorf("Kurzform fehlt in den Beschreibungen: %q", s.desc["store_knowledge"])
	}
	if in := s.instructions(); !strings.Contains(in, "Atrium Tablinum (kurz: Atrium)") || !strings.Contains(in, "„Atrium“") {
		t.Errorf("Server-Hinweise: %q", in)
	}
	if in := New(Deps{}, Settings{ProductName: "Hub"}).instructions(); strings.Contains(in, "kurz:") {
		t.Errorf("ohne Kurzform kein Zusatz: %q", in)
	}
	if !strings.Contains(s.desc["store_knowledge"], "Example Corp") || !strings.Contains(s.desc["handoff_save"], "14 Tagen") {
		t.Error("Variablen nicht ersetzt")
	}
	for name := range defaultDescriptions {
		if s.desc[name] == "" {
			t.Errorf("Beschreibung %s leer", name)
		}
	}
}

// ---- integration over real HTTP with the SDK client ---------------------------------

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
	ctx    context.Context
	srv    *Server
	users  *users.Store
	spaces *spaces.Store
	http   *httptest.Server
	tokens map[string]string // token -> email
}

func setup(t *testing.T, writesPerMinute int) *env {
	t.Helper()
	pool := dbtest.NewPool(t)
	e := &env{ctx: context.Background(), tokens: map[string]string{}}
	e.users = users.New(pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true})
	e.spaces = spaces.New(pool)
	checker := authz.NewChecker(pool, time.Minute)
	e.users.OnChange = checker.InvalidateAll
	e.spaces.OnChange = checker.InvalidateAll
	langs := []string{"german", "english", "simple"}
	ing, err := ingest.New(ingest.Config{MaxDocumentBytes: 5000, Chunk: chunk.Options{TargetTokens: 100, MaxTokens: 150}, FTSLanguages: langs}, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	ds := docs.New(pool, e.spaces, ing, docs.Options{PersonalMaxDocuments: 100, PersonalMaxBytes: 1 << 20, HandoffTTLDays: 14, DuplicateThreshold: 0.9})
	sr, err := search.New(pool, fakeEmbedder{}, search.Config{FTSLanguages: langs, CandidatesPerMethod: 50, RRFK: 60, DefaultLimit: 5, MaxLimit: 20, HNSWEfSearch: 100})
	if err != nil {
		t.Fatal(err)
	}
	e.srv = New(Deps{Users: e.users, Spaces: e.spaces, Docs: ds, Search: sr, Checker: checker, Pool: pool, EmbeddingModel: "fake", EmbeddingDimensions: dbtest.Dimensions,
		Health: []HealthCheck{{Name: "database", Check: func(ctx context.Context) (string, error) { return "", pool.Ping(ctx) }}}},
		Settings{ProductName: "Atrium Tablinum", ShortName: "Atrium", OrganizationName: "Test", HandoffTTLDays: 14, WritesPerMinute: writesPerMinute})

	verifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		email, ok := e.tokens[token]
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		u, err := e.users.Provision(ctx, users.Identity{IDP: "t", Subject: email, Email: email})
		if errors.Is(err, users.ErrDisabled) {
			return nil, auth.ErrInvalidToken // a verifier must answer 401 for disabled accounts
		}
		if err != nil {
			return nil, err
		}
		return &auth.TokenInfo{UserID: u.ID.String(), Expiration: time.Now().Add(time.Hour)}, nil
	}
	e.http = httptest.NewServer(e.srv.Handler(verifier, ""))
	t.Cleanup(e.http.Close)
	return e
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// session connects an MCP client as the given user.
func (e *env) session(t *testing.T, email string) *mcp.ClientSession {
	t.Helper()
	token := "tok-" + email
	e.tokens[token] = email
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-ai", Version: "1"}, nil)
	cs, err := client.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint: e.http.URL, HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool; it returns the structured result as a map, or the error text.
func (e *env) call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(e.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: Protokollfehler %v", tool, err)
	}
	if res.IsError {
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return nil, sb.String()
	}
	b, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return m, ""
}

func (e *env) mustCall(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	m, msg := e.call(t, cs, tool, args)
	if msg != "" {
		t.Fatalf("%s: %s", tool, msg)
	}
	return m
}

func (e *env) team(t *testing.T, slug, name string, approval bool, members map[string]authz.Role) spaces.Space {
	t.Helper()
	sp, err := e.spaces.CreateTeam(e.ctx, spaces.CreateParams{Slug: slug, Name: name, RequireApproval: approval})
	if err != nil {
		t.Fatal(err)
	}
	for email, role := range members {
		u, err := e.users.Provision(e.ctx, users.Identity{IDP: "t", Subject: email, Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.spaces.SetMember(e.ctx, sp.ID, spaces.PrincipalUser, u.ID, role, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	return sp
}

func TestRejectsMissingOrInvalidToken(t *testing.T) {
	e := setup(t, 0)
	for _, h := range []string{"", "Bearer nope", "Basic abc"} {
		req, _ := http.NewRequest(http.MethodPost, e.http.URL, strings.NewReader(`{}`))
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%q: Status %d", h, resp.StatusCode)
		}
	}
}

func TestToolListAndDescriptions(t *testing.T) {
	e := setup(t, 0)
	cs := e.session(t, "anna@x.de")
	res, err := cs.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool
	}
	for name := range defaultDescriptions {
		if strings.HasPrefix(name, "admin_") || strings.HasPrefix(name, "space_") || name == "review_knowledge" {
			continue // Tools für Admins und Kuratoren hängen von der Rolle ab, siehe TestToolListFollowsRole
		}
		if got[name] == nil {
			t.Errorf("Tool %s fehlt", name)
		}
	}
	if d := got["store_knowledge"].Description; !strings.Contains(d, "ausdrücklich") || !strings.Contains(d, "Niemals aus eigenem Antrieb") {
		t.Errorf("store_knowledge-Beschreibung: %q", d)
	}
	if !got["search_knowledge"].Annotations.ReadOnlyHint || got["store_knowledge"].Annotations.ReadOnlyHint {
		t.Error("ReadOnly-Hinweise falsch")
	}
	for name := range got {
		if strings.HasPrefix(name, "admin_") {
			t.Errorf("Admin-Tool %s für normalen Nutzer gelistet", name)
		}
	}
}

func TestStoreDefaultsToPersonalSpaceAndSearchFindsIt(t *testing.T) {
	e := setup(t, 0)
	anna, ben := e.session(t, "anna@x.de"), e.session(t, "ben@x.de")

	out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Deploy", "content": "alpha Deploy läuft über die Pipeline", "tags": []string{"Ops"}})
	if out["result"] != "stored" || out["space"] != "Mein persönlicher Bereich" || out["status"] != "approved" {
		t.Fatalf("%v", out)
	}
	if !strings.Contains(out["message"].(string), "Mein persönlicher Bereich") {
		t.Errorf("Ort wird nicht genannt: %v", out["message"])
	}
	id := out["id"].(string)

	hits := e.mustCall(t, anna, "search_knowledge", map[string]any{"query": "alpha"})["results"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["id"] != id {
		t.Fatalf("Suche: %v", hits)
	}
	if hits := e.mustCall(t, ben, "search_knowledge", map[string]any{"query": "alpha"})["results"].([]any); len(hits) != 0 {
		t.Errorf("fremder Nutzer findet persönlichen Eintrag: %v", hits)
	}
	if _, msg := e.call(t, ben, "get_knowledge", map[string]any{"id": id}); msg != "nicht gefunden" {
		t.Errorf("get fremder Eintrag: %q", msg)
	}
	if _, msg := e.call(t, ben, "get_knowledge", map[string]any{"id": "kein-uuid"}); msg != "nicht gefunden" {
		t.Errorf("get ungültige ID: %q", msg)
	}
	got := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": id})
	if got["title"] != "Deploy" || !strings.Contains(got["content"].(string), "Pipeline") {
		t.Errorf("get: %v", got)
	}
	list := e.mustCall(t, anna, "list_knowledge", map[string]any{"tag": "OPS"})["entries"].([]any)
	if len(list) != 1 {
		t.Errorf("list: %v", list)
	}
}

func TestStoreWithSpaceReferences(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Kundensupport", false, map[string]authz.Role{"anna@x.de": authz.Author})
	e.team(t, "support-intern", "Support Intern", false, map[string]authz.Role{"anna@x.de": authz.Author})
	e.team(t, "lesen", "Nur Lesen", false, map[string]authz.Role{"anna@x.de": authz.Reader})
	e.team(t, "gf", "Geschäftsführung", false, map[string]authz.Role{"boss@x.de": authz.Author})
	anna := e.session(t, "anna@x.de")

	ok := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "inhalt", "space": "kundensupport"})
	if ok["result"] != "stored" || ok["space"] != "Kundensupport" {
		t.Fatalf("%v", ok)
	}
	// mehrdeutig -> Rückfrage mit Liste, nichts gespeichert
	amb := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T2", "content": "inhalt2", "space": "supp"})
	if amb["result"] != "choose_space" || len(amb["spaces"].([]any)) < 2 {
		t.Fatalf("%v", amb)
	}
	// Reader-Bereich und unsichtbarer Bereich: gleiche Antwort, keine Namen von unsichtbaren Bereichen
	for _, ref := range []string{"nur lesen", "geschäftsführung", "gibtsnicht"} {
		out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T3", "content": "x", "space": ref})
		if out["result"] != "choose_space" {
			t.Errorf("%q: %v", ref, out)
		}
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "Geschäftsführung") || strings.Contains(string(b), "Nur Lesen") {
			t.Errorf("%q: Antwort nennt nicht beschreibbare Bereiche: %s", ref, b)
		}
	}
	// nichts davon wurde gespeichert
	list := e.mustCall(t, anna, "list_knowledge", map[string]any{"space": "support-intern"})["entries"].([]any)
	if len(list) != 0 {
		t.Errorf("Eintrag trotz Rückfrage gespeichert: %v", list)
	}
}

func TestDefaultSpaceSetting(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"anna@x.de": authz.Author})
	e.team(t, "lesen", "Nur Lesen", false, map[string]authz.Role{"anna@x.de": authz.Reader})
	anna := e.session(t, "anna@x.de")

	if _, msg := e.call(t, anna, "set_default_space", map[string]any{"space": "lesen"}); !strings.Contains(msg, "nicht beschreibbar") {
		t.Errorf("Reader-Bereich als Standard: %q", msg)
	}
	e.mustCall(t, anna, "set_default_space", map[string]any{"space": "support"})
	if out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "x"}); out["space"] != "Support" {
		t.Errorf("Standardbereich nicht genutzt: %v", out)
	}
	spaces := e.mustCall(t, anna, "list_spaces", nil)["spaces"].([]any)
	var defaults int
	for _, sp := range spaces {
		if sp.(map[string]any)["is_default"] == true {
			defaults++
		}
	}
	if len(spaces) != 3 || defaults != 1 {
		t.Errorf("list_spaces: %d Bereiche, %d Standard", len(spaces), defaults)
	}
	e.mustCall(t, anna, "set_default_space", map[string]any{})
	if out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T2", "content": "y"}); out["space"] != "Mein persönlicher Bereich" {
		t.Errorf("Zurücksetzen: %v", out)
	}
}

func TestGuardAndDuplicatesAndUpdateArchive(t *testing.T) {
	e := setup(t, 0)
	anna := e.session(t, "anna@x.de")

	if _, msg := e.call(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "password: Tr0ub4dor&3"}); !strings.Contains(msg, "Passwort") {
		t.Errorf("Secret-Guard: %q", msg)
	}
	first := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Erst", "content": "alpha eins"})["id"].(string)
	dup := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Zweit", "content": "alpha zwei"})
	if dup["result"] != "similar_found" || dup["id"] != nil {
		t.Fatalf("Duplikat: %v", dup)
	}
	if e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Zweit", "content": "alpha zwei", "force": true})["result"] != "stored" {
		t.Error("force")
	}
	up := e.mustCall(t, anna, "update_knowledge", map[string]any{"id": first, "title": "Erst neu"})
	if up["title"] != "Erst neu" || up["version"].(float64) != 2 {
		t.Errorf("update: %v", up)
	}
	if _, msg := e.call(t, anna, "update_knowledge", map[string]any{"id": first}); !strings.Contains(msg, "nichts") {
		t.Errorf("leeres update: %q", msg)
	}
	if _, msg := e.call(t, anna, "archive_knowledge", map[string]any{"id": first}); msg == "" {
		// confirm fehlt im Schema -> Protokollfehler oder Tool-Fehler, beides ist abgelehnt
		t.Error("archive ohne confirm")
	}
	if _, msg := e.call(t, anna, "archive_knowledge", map[string]any{"id": first, "confirm": false}); !strings.Contains(msg, "Bestätigung") {
		t.Errorf("archive confirm=false: %q", msg)
	}
	e.mustCall(t, anna, "archive_knowledge", map[string]any{"id": first, "confirm": true})
	if _, msg := e.call(t, anna, "get_knowledge", map[string]any{"id": first}); msg != "nicht gefunden" {
		t.Errorf("archivierter Eintrag: %q", msg)
	}
}

func TestApprovalSpaceAndReaderRights(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "wiki", "Wiki", true, map[string]authz.Role{"author@x.de": authz.Author, "reader@x.de": authz.Reader, "cur@x.de": authz.Curator})
	author, reader := e.session(t, "author@x.de"), e.session(t, "reader@x.de")

	out := e.mustCall(t, author, "store_knowledge", map[string]any{"title": "Neu", "content": "alpha", "space": "wiki"})
	if out["status"] != "draft" || !strings.Contains(out["message"].(string), "Entwurf") {
		t.Fatalf("%v", out)
	}
	if hits := e.mustCall(t, reader, "search_knowledge", map[string]any{"query": "alpha"})["results"].([]any); len(hits) != 0 {
		t.Errorf("Reader sieht Entwurf: %v", hits)
	}
	if hits := e.mustCall(t, author, "search_knowledge", map[string]any{"query": "alpha"})["results"].([]any); len(hits) != 1 ||
		hits[0].(map[string]any)["status"] != "draft" {
		t.Errorf("Autor sieht eigenen Entwurf nicht als Entwurf: %v", hits)
	}
}

func TestHandoffRoundTrip(t *testing.T) {
	e := setup(t, 0)
	claude, other := e.session(t, "anna@x.de"), e.session(t, "anna@x.de") // zweiter Assistent, gleicher Nutzer
	saved := e.mustCall(t, claude, "handoff_save", map[string]any{"title": "Stand", "content": "Ziel: X. Nächster Schritt: Y."})
	if saved["id"] == "" || saved["expires_at"] == "" {
		t.Fatalf("%v", saved)
	}
	list := e.mustCall(t, other, "handoff_list", nil)["handoffs"].([]any)
	if len(list) != 1 {
		t.Fatalf("list: %v", list)
	}
	loaded := e.mustCall(t, other, "handoff_load", nil)
	if !strings.Contains(loaded["content"].(string), "Nächster Schritt") || loaded["id"] != saved["id"] {
		t.Errorf("load: %v", loaded)
	}
	// Übergaben tauchen weder in der Suche noch in der Wissensliste auf
	if hits := e.mustCall(t, other, "search_knowledge", map[string]any{"query": "Nächster Schritt"})["results"].([]any); len(hits) != 0 {
		t.Errorf("Übergabe in Suche: %v", hits)
	}
	if l := e.mustCall(t, other, "list_knowledge", nil)["entries"].([]any); len(l) != 0 {
		t.Errorf("Übergabe in Liste: %v", l)
	}
	third := e.session(t, "ben@x.de")
	if _, msg := e.call(t, third, "handoff_load", map[string]any{"id": saved["id"]}); msg != "nicht gefunden" {
		t.Errorf("fremde Übergabe: %q", msg)
	}
	if _, msg := e.call(t, third, "handoff_load", nil); msg != "nicht gefunden" {
		t.Errorf("keine eigene Übergabe: %q", msg)
	}
}

func TestDisabledUserIsLockedOutImmediately(t *testing.T) {
	e := setup(t, 0)
	anna := e.session(t, "anna@x.de")
	e.mustCall(t, anna, "list_spaces", nil)
	u, _ := e.users.GetByEmail(e.ctx, "anna@x.de")
	if err := e.users.SetActive(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	// the verifier rejects the token (401), so the running session cannot continue
	if _, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "list_spaces"}); err == nil {
		t.Error("deaktivierter Nutzer konnte mit laufender Sitzung weiterarbeiten")
	}
}

func TestWriteRateLimit(t *testing.T) {
	e := setup(t, 2)
	anna := e.session(t, "anna@x.de")
	for i := 0; i < 2; i++ {
		e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "beta " + strings.Repeat("x", i+1), "force": true})
	}
	if _, msg := e.call(t, anna, "store_knowledge", map[string]any{"title": "T", "content": "gamma", "force": true}); !strings.Contains(msg, "zu viele") {
		t.Errorf("Rate Limit: %q", msg)
	}
	// Lesen bleibt möglich
	e.mustCall(t, anna, "search_knowledge", map[string]any{"query": "beta"})
}
