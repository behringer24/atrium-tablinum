package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/notify"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

type stubEmbedder struct{}

func (stubEmbedder) Model() string              { return "stub" }
func (stubEmbedder) Dimensions() int            { return dbtest.Dimensions }
func (stubEmbedder) Ping(context.Context) error { return nil }
func (stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0, 0, 1, 0}
	}
	return out, nil
}

type sentMail struct{ to, body string }

type flow struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	users   *users.Store
	spaces  *spaces.Store
	checker *authz.Checker
	docs    *docs.Store
	svc     *notify.Service

	mu     sync.Mutex
	mails  []sentMail
	hooks  []map[string]any
	status int // answer of the web hook
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	f := &flow{t: t, ctx: context.Background(), pool: dbtest.NewPool(t), status: 200}
	f.users = users.New(f.pool, users.Options{BootstrapAdmins: []string{"boss@x.de"}, PersonalEnabled: true})
	f.spaces = spaces.New(f.pool)
	f.checker = authz.NewChecker(f.pool, time.Minute)
	f.users.OnChange = f.checker.InvalidateAll
	f.spaces.OnChange = f.checker.InvalidateAll

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status == 200 {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			f.hooks = append(f.hooks, m)
		}
		w.WriteHeader(f.status)
	}))
	t.Cleanup(srv.Close)

	f.svc = notify.New(f.pool, f.users, f.checker, f.spaces, notify.Options{
		Enabled: true, Instance: "Atrium", AllowPrivateNetworks: true,
		Connections: []notify.Connection{
			{Name: "auto", Type: "webhook", URL: srv.URL, Secret: "0123456789abcdef"},
			{Name: "mail", Type: "smtp", Host: "mail.test", Port: 587, TLS: "starttls", From: "atrium@x.de"},
		},
		MaxWebhooks: 1, MaxMail: 1,
		Mailer: func(_ context.Context, _ notify.Connection, to string, msg []byte) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.mails = append(f.mails, sentMail{to, string(msg)})
			return nil
		},
	})
	ing, err := ingest.New(ingest.Config{MaxDocumentBytes: 2000, Chunk: chunk.Options{TargetTokens: 50, MaxTokens: 80},
		FTSLanguages: []string{"english", "simple"}}, stubEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	f.docs = docs.New(f.pool, f.spaces, ing, docs.Options{HandoffTTLDays: 14, DuplicateThreshold: 0.9, InlineIndexSections: -1, Notify: f.svc})
	return f
}

func (f *flow) actor(email string) docs.Actor {
	f.t.Helper()
	u, err := f.users.Provision(f.ctx, users.Identity{EmailVerified: true, IDP: "g", Subject: email, Email: email})
	if err != nil {
		f.t.Fatal(err)
	}
	return f.refresh(u)
}

func (f *flow) refresh(u users.User) docs.Actor {
	f.t.Helper()
	u, err := f.users.Get(f.ctx, u.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	r, err := f.checker.EffectiveRoles(f.ctx, u)
	if err != nil {
		f.t.Fatal(err)
	}
	return docs.Actor{User: u, Roles: r, Client: "claude"}
}

func (f *flow) member(sp spaces.Space, a docs.Actor, role authz.Role) docs.Actor {
	f.t.Helper()
	if err := f.spaces.SetMember(f.ctx, sp.ID, spaces.PrincipalUser, a.User.ID, role, a.User.ID); err != nil {
		f.t.Fatal(err)
	}
	return f.refresh(a.User)
}

// run routes and sends; the merge window is skipped by making everything due at once.
func (f *flow) run() {
	f.t.Helper()
	if err := f.svc.Run(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE notification_deliveries SET send_after = now() WHERE status = 'pending'`); err != nil {
		f.t.Fatal(err)
	}
	if err := f.svc.Run(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *flow) choose(conn string, enabled bool, mode string, events ...string) {
	f.t.Helper()
	p := notify.Patch{Enabled: &enabled, Events: &events}
	if mode != "" {
		p.Mode = &mode
	}
	if _, _, err := f.svc.SetSetting(f.ctx, conn, p, uuid.New()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *flow) mailTo() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, m := range f.mails {
		out[m.to] += m.body
	}
	return out
}

func (f *flow) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestReviewWorkflowNotifiesByRole(t *testing.T) {
	f := newFlow(t)
	sp, err := f.spaces.CreateTeam(f.ctx, spaces.CreateParams{Slug: "ops", Name: "Operations", RequireApproval: true, DefaultRole: authz.Reader})
	if err != nil {
		t.Fatal(err)
	}
	boss := f.actor("boss@x.de")
	curator := f.member(sp, f.actor("cora@x.de"), authz.Curator)
	author := f.member(sp, f.actor("anna@x.de"), authz.Author)
	_ = f.actor("rita@x.de") // reads only, with the default role of the space
	f.choose("auto", true, "", "draft_waiting")
	f.choose("mail", true, "", "draft_waiting", "draft_approved", "draft_returned")

	// Three drafts arrive close together: one notice each, with the number.
	var ids []uuid.UUID
	for _, title := range []string{"One", "Two", "Three"} {
		res, err := f.docs.Store(f.ctx, author, docs.StoreParams{SpaceID: sp.ID, Title: title, Content: "text " + title, Force: true})
		if err != nil || res.Document == nil || res.Document.Status != "draft" {
			t.Fatalf("Store: %v %+v", err, res)
		}
		ids = append(ids, res.Document.ID)
	}
	f.run()

	mails := f.mailTo()
	if len(mails) != 2 || mails["cora@x.de"] == "" || mails["boss@x.de"] == "" {
		t.Fatalf("curators and instance admins are told, nobody else: %v", keys(mails))
	}
	if !strings.Contains(mails["cora@x.de"], "3 drafts wait for review in the space Operations") {
		t.Errorf("the mail: %q", mails["cora@x.de"])
	}
	for _, title := range []string{"One", "Two", "Three", "text One"} {
		if strings.Contains(mails["cora@x.de"], title) {
			t.Errorf("a mail must name no content, found %q", title)
		}
	}
	if len(f.hooks) != 1 || f.hooks[0]["type"] != "draft_waiting" || f.hooks[0]["count"] != float64(3) {
		t.Fatalf("one web hook call with the number: %v", f.hooks)
	}
	if _, named := f.hooks[0]["space"]; named {
		t.Errorf("a web hook names the space only when it is set to")
	}

	// The curator decides: the author is told, the curator is not told of their own decision.
	f.mails = nil
	if _, err := f.docs.Review(f.ctx, curator, docs.ReviewParams{ID: ids[0], Approve: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Review(f.ctx, curator, docs.ReviewParams{ID: ids[1], Note: "needs a source"}); err != nil {
		t.Fatal(err)
	}
	f.run()
	mails = f.mailTo()
	if len(mails) != 1 || !strings.Contains(mails["anna@x.de"], "approved") || !strings.Contains(mails["anna@x.de"], "returned to you with a note") {
		t.Fatalf("the author hears of both decisions: %v", mails)
	}
	if strings.Contains(mails["anna@x.de"], "needs a source") {
		t.Errorf("the note of a return is not part of the notice")
	}

	// Nobody is told of their own draft, and a curator's own entry in an approval space is a draft too.
	f.mails = nil
	if _, err := f.docs.Store(f.ctx, curator, docs.StoreParams{SpaceID: sp.ID, Title: "Own", Content: "own text", Force: true}); err != nil {
		t.Fatal(err)
	}
	f.run()
	if m := f.mailTo(); m["cora@x.de"] != "" || m["boss@x.de"] == "" {
		t.Errorf("the curator is not told of their own draft, the admin is: %v", keys(m))
	}
	_ = boss
}

func TestNothingIsSentUntilAnAdminSwitchesAConnectionOn(t *testing.T) {
	f := newFlow(t)
	sp, _ := f.spaces.CreateTeam(f.ctx, spaces.CreateParams{Slug: "ops", Name: "Operations", RequireApproval: true, DefaultRole: authz.Reader})
	author := f.member(sp, f.actor("anna@x.de"), authz.Author)
	if _, err := f.docs.Store(f.ctx, author, docs.StoreParams{SpaceID: sp.ID, Title: "T", Content: "text", Force: true}); err != nil {
		t.Fatal(err)
	}
	f.run()
	if len(f.mails) != 0 || len(f.hooks) != 0 || f.count(`SELECT count(*) FROM notification_deliveries`) != 0 {
		t.Errorf("no connection is on: nothing is planned or sent")
	}
	if f.count(`SELECT count(*) FROM notification_events WHERE routed_at IS NULL`) != 0 {
		t.Errorf("the event must still be marked as routed")
	}
}

func TestDigestWaitsForItsTimeAndMergesPerPerson(t *testing.T) {
	f := newFlow(t)
	sp, _ := f.spaces.CreateTeam(f.ctx, spaces.CreateParams{Slug: "ops", Name: "Operations", RequireApproval: true, DefaultRole: authz.Reader})
	author := f.member(sp, f.actor("anna@x.de"), authz.Author)
	f.member(sp, f.actor("cora@x.de"), authz.Curator)
	f.choose("mail", true, "digest", "draft_waiting")
	for _, title := range []string{"One", "Two"} {
		if _, err := f.docs.Store(f.ctx, author, docs.StoreParams{SpaceID: sp.ID, Title: title, Content: "text " + title, Force: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.mails) != 0 {
		t.Fatalf("a digest is not sent at once")
	}
	var at time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT min(send_after) FROM notification_deliveries`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at.UTC().Format("15:04") != "08:00" {
		t.Errorf("the digest goes out at 08:00 UTC, not %v", at)
	}
}

func TestWebhookFailuresAreRetriedAndGivenUp(t *testing.T) {
	f := newFlow(t)
	sp, _ := f.spaces.CreateTeam(f.ctx, spaces.CreateParams{Slug: "ops", Name: "Operations", RequireApproval: true, DefaultRole: authz.Reader})
	author := f.member(sp, f.actor("anna@x.de"), authz.Author)
	f.choose("auto", true, "", "draft_waiting")
	if _, err := f.docs.Store(f.ctx, author, docs.StoreParams{SpaceID: sp.ID, Title: "T", Content: "text", Force: true}); err != nil {
		t.Fatal(err)
	}

	f.status = 503
	f.run()
	if n := f.count(`SELECT count(*) FROM notification_deliveries WHERE status = 'pending' AND attempts = 1 AND last_error LIKE '%503%'`); n != 1 {
		t.Fatalf("a failing receiver leaves the delivery waiting for the next try")
	}
	st, err := f.svc.Stats(f.ctx)
	if err != nil || st["auto"].Pending != 1 || st["auto"].LastError == "" {
		t.Errorf("stats: %+v %v", st["auto"], err)
	}

	f.status = 200
	f.run()
	if n := f.count(`SELECT count(*) FROM notification_deliveries WHERE status = 'sent'`); n != 1 || len(f.hooks) != 1 {
		t.Fatalf("it is sent once the receiver is back: %d %v", n, f.hooks)
	}

	// A refused address is given up at once.
	if _, err := f.docs.Store(f.ctx, author, docs.StoreParams{SpaceID: sp.ID, Title: "U", Content: "other text", Force: true}); err != nil {
		t.Fatal(err)
	}
	f.status = 404
	f.run()
	if n := f.count(`SELECT count(*) FROM notification_deliveries WHERE status = 'failed'`); n != 1 {
		t.Fatalf("404 is not retried")
	}
	if sum, err := f.svc.Summary(f.ctx); err == nil {
		t.Errorf("a given-up notice must show in the health check, got %q", sum)
	}
}

func TestOnlyTheFirstConnectionOfAKindIsUsed(t *testing.T) {
	f := newFlow(t)
	svc := notify.New(f.pool, f.users, f.checker, f.spaces, notify.Options{Enabled: true, MaxWebhooks: 1, MaxMail: 1,
		Connections: []notify.Connection{
			{Name: "one", Type: "slack", URL: "https://hooks.slack.com/services/a"},
			{Name: "two", Type: "webhook", URL: "https://x.example.org/h", Secret: "0123456789abcdef"},
			{Name: "mail", Type: "smtp", Host: "mail.test", Port: 587, TLS: "starttls", From: "a@x.de"},
			{Name: "mail2", Type: "smtp", Host: "mail2.test", Port: 587, TLS: "starttls", From: "a@x.de"},
		}})
	active, left := svc.Connections()
	if len(active) != 2 || active[0].Name != "one" || active[1].Name != "mail" || strings.Join(left, ",") != "two,mail2" {
		t.Errorf("active %v, left out %v", active, left)
	}
	if _, _, err := svc.SetSetting(f.ctx, "two", notify.Patch{}, uuid.New()); err != notify.ErrUnknownConnection {
		t.Errorf("a connection that is left out cannot be set: %v", err)
	}
}

func TestSettingsAreChecked(t *testing.T) {
	f := newFlow(t)
	yes := true
	for what, p := range map[string]notify.Patch{
		"an unknown event":       {Events: &[]string{"everything"}},
		"on without events":      {Enabled: &yes},
		"a bad mode":             {Mode: ptr("hourly")},
		"a bad digest time":      {DigestAt: ptr("25:00")},
		"a digest time with day": {DigestAt: ptr("8am")},
	} {
		if _, _, err := f.svc.SetSetting(f.ctx, "mail", p, uuid.New()); err == nil {
			t.Errorf("%s must be refused", what)
		}
	}
	if _, _, err := f.svc.SetSetting(f.ctx, "mail", notify.Patch{Enabled: &yes, Events: &[]string{"draft_waiting", "draft_waiting", "health_changed"}}, uuid.New()); err != nil {
		t.Fatal(err)
	}
	all, err := f.svc.Settings(f.ctx)
	if err != nil || len(all["mail"].Events) != 2 || !all["mail"].Enabled || all["mail"].Mode != "instant" {
		t.Errorf("settings: %+v %v", all["mail"], err)
	}
}

func TestHealthWatcherReportsChangesNotTheFirstState(t *testing.T) {
	f := newFlow(t)
	f.choose("auto", true, "", "health_changed")
	failing := true
	watch := f.svc.WatchHealth([]notify.Check{{Name: "embedder", Run: func(context.Context) (string, error) {
		if failing {
			return "", context.DeadlineExceeded
		}
		return "", nil
	}}})
	events := func() int { return f.count(`SELECT count(*) FROM notification_events WHERE kind = 'health_changed'`) }
	if err := watch(f.ctx); err != nil || events() != 0 {
		t.Fatalf("the first run only learns the state: %d %v", events(), err)
	}
	failing = false
	if err := watch(f.ctx); err != nil || events() != 1 {
		t.Fatalf("a change is reported: %d %v", events(), err)
	}
	if err := watch(f.ctx); err != nil || events() != 1 {
		t.Fatalf("no change, no event: %d %v", events(), err)
	}
	f.run()
	if len(f.hooks) != 1 || f.hooks[0]["type"] != "health_changed" || f.hooks[0]["detail"] != "embedder ok" {
		t.Fatalf("hooks: %v", f.hooks)
	}
	if strings.Contains(f.hooks[0]["message"].(string), "deadline") {
		t.Errorf("the error text must not leave the server")
	}
}

func ptr[T any](v T) *T { return &v }

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
