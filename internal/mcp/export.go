package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/export"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// exportLinkTTL is how long a download link works. The link is a credential: whoever has it
// can download once, so it is short-lived.
const exportLinkTTL = 10 * time.Minute

// maxParallelExports bounds the exports that run at the same time, so that a few large ones
// cannot starve the server.
const maxParallelExports = 2

// exportAll in a link means: every team space the user administers at the time of the download.
const exportAll = "*"

// exportLinks issues and redeems the signed, single-use download links. The key is random per
// process, so links do not survive a restart, which is fine for ten minutes.
type exportLinks struct {
	key  []byte
	mu   sync.Mutex
	used map[string]time.Time // nonce -> expiry of the link
}

func newExportLinks() *exportLinks {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("no randomness: " + err.Error())
	}
	return &exportLinks{key: key, used: map[string]time.Time{}}
}

type linkClaims struct {
	User  uuid.UUID `json:"u"`
	Scope string    `json:"s"` // a space id or exportAll
	Exp   int64     `json:"e"`
	Nonce string    `json:"n"`
}

func (l *exportLinks) sign(payload string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// issue returns a token for the user and scope.
func (l *exportLinks) issue(user uuid.UUID, scope string, now time.Time) string {
	n := make([]byte, 12)
	if _, err := rand.Read(n); err != nil {
		panic("no randomness: " + err.Error())
	}
	b, _ := json.Marshal(linkClaims{User: user, Scope: scope, Exp: now.Add(exportLinkTTL).Unix(), Nonce: base64.RawURLEncoding.EncodeToString(n)})
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + l.sign(payload)
}

var errBadLink = errors.New("link is not valid or expired")

// check verifies signature and expiry without using the link up.
func (l *exportLinks) check(token string, now time.Time) (linkClaims, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(l.sign(payload))) {
		return linkClaims{}, errBadLink
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return linkClaims{}, errBadLink
	}
	var c linkClaims
	if err := json.Unmarshal(raw, &c); err != nil || now.Unix() >= c.Exp {
		return linkClaims{}, errBadLink
	}
	return c, nil
}

// consume marks the link as used; it reports false if it was used before.
func (l *exportLinks) consume(c linkClaims, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for n, exp := range l.used {
		if now.Unix() >= exp.Unix() {
			delete(l.used, n)
		}
	}
	if _, done := l.used[c.Nonce]; done {
		return false
	}
	l.used[c.Nonce] = time.Unix(c.Exp, 0)
	return true
}

// ---- tool ------------------------------------------------------------------------------

type exportIn struct {
	Space string `json:"space,omitempty" jsonschema:"Space to export (slug or name, or 'my personal space'); empty = all team spaces the user administers"`
}

type exportOut struct {
	URL              string   `json:"url"`
	ExpiresInMinutes int      `json:"expires_in_minutes"`
	Spaces           []string `json:"spaces"`
}

// exportSpacesFor resolves what a user may export: the spaces named by ref among those they
// administer, or, without ref, all team spaces they administer. A personal space is exported
// only when named, and only by its owner (nobody else holds a role in it).
func (s *Server) exportSpacesFor(ctx context.Context, a docs.Actor, ref string) ([]spaces.Space, error) {
	admin, err := s.spacesAtLeast(ctx, a, authz.Admin)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) != "" {
		sp, cands := matchSpace(admin, ref)
		switch {
		case sp != nil:
			return []spaces.Space{*sp}, nil
		case len(cands) > 1:
			return nil, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", ref, spaceNames(cands)))
		}
		return nil, userErr("There is no space of that name that you administer. You can export spaces you are admin of, and your own personal space.")
	}
	var team []spaces.Space
	for _, sp := range admin {
		if sp.Type == "team" {
			team = append(team, sp)
		}
	}
	if len(team) == 0 {
		return nil, userErr("You administer no team space. To export your own notes, name your personal space.")
	}
	return team, nil
}

func (s *Server) exportKnowledge(ctx context.Context, c *call, in exportIn) (exportOut, error) {
	list, err := s.exportSpacesFor(ctx, c.Actor, in.Space)
	if err != nil {
		return exportOut{}, err
	}
	scope := exportAll
	if strings.TrimSpace(in.Space) != "" {
		scope = list[0].ID.String()
	}
	token := s.exports.issue(c.Actor.User.ID, scope, time.Now())
	out := exportOut{URL: strings.TrimRight(s.set.BaseURL, "/") + "/export/" + token, ExpiresInMinutes: int(exportLinkTTL / time.Minute)}
	for _, sp := range list {
		out.Spaces = append(out.Spaces, spaceDisplayName(sp))
	}
	return out, nil
}

// ---- download --------------------------------------------------------------------------

// ExportHandler serves GET /export/{token}. The signed link replaces the bearer token, because
// the person who downloads uses a browser, not an assistant. Everything is checked again at the
// download: the account must be active and the user must still administer the space.
func (s *Server) ExportHandler() http.Handler {
	return http.HandlerFunc(s.serveExport)
}

func (s *Server) serveExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	ctx := r.Context()
	now := time.Now()
	claims, err := s.exports.check(r.PathValue("token"), now)
	if err != nil {
		http.Error(w, "this link is not valid or has expired", http.StatusNotFound)
		return
	}
	u, err := s.Users.Get(ctx, claims.User)
	if err != nil || !u.Active {
		http.Error(w, "this link is not valid or has expired", http.StatusNotFound)
		return
	}
	if !s.reads.allow(u.ID) {
		http.Error(w, "too many requests in a short time, please try again in a minute", http.StatusTooManyRequests)
		return
	}
	roles, err := s.Checker.EffectiveRoles(ctx, u)
	if err != nil {
		slog.Error("Roles could not be determined", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a := docs.Actor{User: u, Roles: roles, Client: "export"}
	list, err := s.exportSpacesForScope(ctx, a, claims.Scope)
	if err != nil || len(list) == 0 {
		http.Error(w, "this link is not valid or has expired", http.StatusNotFound)
		return
	}
	select {
	case s.exportSlots <- struct{}{}:
		defer func() { <-s.exportSlots }()
	default:
		w.Header().Set("Retry-After", "30")
		http.Error(w, "other exports are running, please try again in a moment", http.StatusServiceUnavailable)
		return
	}
	if !s.exports.consume(claims, now) {
		http.Error(w, "this link is not valid or has expired", http.StatusNotFound)
		return
	}
	name := "atrium-export-" + now.UTC().Format("2006-01-02") + ".zip"
	if claims.Scope != exportAll && len(list) == 1 {
		name = "atrium-" + list[0].Slug + "-" + now.UTC().Format("2006-01-02") + ".zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	res, err := export.Write(ctx, w, s.Docs, a, list, now)
	if err != nil {
		// Part of the archive is already sent; cutting the connection leaves a ZIP that does
		// not open instead of one that looks complete.
		slog.Error("Export failed", "user", u.ID, "error", err)
		panic(http.ErrAbortHandler)
	}
	uid := u.ID
	for _, sc := range res.Spaces {
		sid := sc.Space.ID
		if err := audit.Insert(ctx, s.Pool, audit.Event{UserID: &uid, Client: "export", Action: "knowledge_export", SpaceID: &sid,
			Personal: sc.Space.Type == "personal", Details: map[string]any{"entries": sc.Entries, "via": "link"}}); err != nil {
			slog.Error("Audit entry for an export failed", "error", err)
		}
	}
}

// exportSpacesForScope resolves the scope of a link at download time.
func (s *Server) exportSpacesForScope(ctx context.Context, a docs.Actor, scope string) ([]spaces.Space, error) {
	if scope == exportAll {
		list, err := s.spacesAtLeast(ctx, a, authz.Admin)
		if err != nil {
			return nil, err
		}
		var team []spaces.Space
		for _, sp := range list {
			if sp.Type == "team" {
				team = append(team, sp)
			}
		}
		return team, nil
	}
	id, err := uuid.Parse(scope)
	if err != nil || !a.Roles.Can(id, authz.Admin) {
		return nil, docs.ErrNotFound
	}
	sp, err := s.Spaces.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return []spaces.Space{sp}, nil
}
