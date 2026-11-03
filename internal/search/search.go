// Package search runs the hybrid (vector + full text) search with the permission filter
// inside the SQL query, fused by reciprocal rank fusion (concept section 11).
package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

var ErrInvalid = errors.New("invalid search request")

const maxQueryRunes = 500

var langRe = regexp.MustCompile(`^[a-z_]{2,30}$`)

type Config struct {
	FTSLanguages        []string
	CandidatesPerMethod int
	RRFK                int
	DefaultLimit        int
	MaxLimit            int
	HNSWEfSearch        int
}

// Caller identifies who searches: their visibility scope plus audit context.
type Caller struct {
	Scope ingest.Scope
	// PersonalSpace is the caller's own personal space, if any. Searches that include it
	// are audited without the query text.
	PersonalSpace *uuid.UUID
	Client        string
}

type Params struct {
	Query string
	// Spaces restricts the search to these spaces (intersected with what the caller
	// may see; unknown or invisible ids simply match nothing).
	Spaces []uuid.UUID
	Tags   []string
	Limit  int
	// Archive searches only the archive instead of the active entries. Archived entries
	// are visible to their author and the curators of the space, nobody else.
	Archive bool
}

type Result struct {
	ChunkID     uuid.UUID
	DocumentID  uuid.UUID
	SpaceID     uuid.UUID
	SpaceName   string
	SpaceType   string
	Title       string
	HeadingPath string
	Content     string
	Status      string // approved | draft | rejected | archived – all but approved only for author/curators
	AuthorEmail string
	UpdatedAt   time.Time
	Score       float64
}

type Service struct {
	pool *pgxpool.Pool
	emb  embed.Embedder
	cfg  Config
	tsq  string // SQL expression building the tsquery from $2
}

func New(pool *pgxpool.Pool, emb embed.Embedder, cfg Config) (*Service, error) {
	if len(cfg.FTSLanguages) == 0 {
		return nil, errors.New("at least one FTS language is required")
	}
	parts := make([]string, len(cfg.FTSLanguages))
	for i, l := range cfg.FTSLanguages {
		if !langRe.MatchString(l) { // inlined into SQL below
			return nil, fmt.Errorf("invalid FTS language %q", l)
		}
		parts[i] = fmt.Sprintf("websearch_to_tsquery('%s'::regconfig, $2)", l)
	}
	if cfg.CandidatesPerMethod < 1 || cfg.RRFK < 1 || cfg.DefaultLimit < 1 || cfg.MaxLimit < cfg.DefaultLimit {
		return nil, errors.New("invalid search configuration")
	}
	return &Service{pool: pool, emb: emb, cfg: cfg, tsq: strings.Join(parts, " || ")}, nil
}

func (s *Service) sql(archive bool) string {
	visible := visibility.Clause("d", 3, 4, 5)
	if archive {
		visible = visibility.ArchiveClause("d", 3, 4, 5)
	}
	return `
WITH q AS (SELECT (` + s.tsq + `) AS tsq),
vis AS (
    SELECT d.id FROM documents d
    WHERE ` + visible + `
      AND d.kind = 'knowledge'
      AND ($9::text[] IS NULL OR d.tags && $9::text[])
),
vec AS (
    SELECT c.id, row_number() OVER (ORDER BY c.embedding <=> $1::vector) AS rn
    FROM chunks c JOIN vis ON vis.id = c.document_id
    WHERE $1::vector IS NOT NULL
    ORDER BY c.embedding <=> $1::vector
    LIMIT $6
),
fts AS (
    SELECT c.id, row_number() OVER (ORDER BY ts_rank_cd(c.tsv, q.tsq) DESC) AS rn
    FROM chunks c JOIN vis ON vis.id = c.document_id, q
    WHERE c.tsv @@ q.tsq
    ORDER BY ts_rank_cd(c.tsv, q.tsq) DESC
    LIMIT $6
),
fused AS (
    SELECT id, SUM(1.0 / ($7::int + rn)) AS score
    FROM (SELECT * FROM vec UNION ALL SELECT * FROM fts) r
    GROUP BY id ORDER BY score DESC LIMIT $8
)
SELECT c.id, d.id, d.space_id, sp.name, sp.type, d.title, c.heading_path, c.content, d.status,
       au.email::text, d.updated_at, f.score::float8
FROM fused f
JOIN chunks c    ON c.id = f.id
JOIN documents d ON d.id = c.document_id
JOIN spaces sp   ON sp.id = d.space_id
JOIN users au    ON au.id = d.author_id
ORDER BY f.score DESC, c.id`
}

// Search returns the best matching chunks the caller may see. If the embedder is
// unavailable it degrades to full-text only instead of failing.
func (s *Service) Search(ctx context.Context, c Caller, p Params) ([]Result, error) {
	q := strings.TrimSpace(p.Query)
	if q == "" || utf8.RuneCountInString(q) > maxQueryRunes || strings.ContainsRune(q, 0) {
		return nil, fmt.Errorf("%w: query text is missing or longer than %d characters", ErrInvalid, maxQueryRunes)
	}
	limit := p.Limit
	if limit < 1 {
		limit = s.cfg.DefaultLimit
	}
	limit = min(limit, s.cfg.MaxLimit)

	visible := restrict(c.Scope.Visible, p.Spaces)
	var results []Result
	if len(visible) > 0 {
		var err error
		if results, err = s.run(ctx, c, q, visible, p.Tags, limit, p.Archive); err != nil {
			return nil, err
		}
	}
	s.audit(ctx, c, q, visible, len(results), p.Archive)
	return results, nil
}

func (s *Service) run(ctx context.Context, c Caller, q string, visible []uuid.UUID, tags []string, limit int, archive bool) ([]Result, error) {
	var vec *string
	if v, err := embed.Query(ctx, s.emb, q); err != nil {
		slog.Warn("query embedding failed, using full-text search only", "error", err)
	} else {
		lit := ingest.VectorLiteral(v)
		vec = &lit
	}

	var tagFilter []string // nil = no tag filter
	if len(tags) > 0 {
		var err error
		if tagFilter, err = ingest.NormalizeTags(tags); err != nil {
			return nil, err
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// ef_search is a number from our own config; SET LOCAL cannot take parameters.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL hnsw.ef_search = %d`, max(s.cfg.HNSWEfSearch, 1))); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`); err != nil {
		return nil, err
	}

	curator := c.Scope.Curator
	if curator == nil {
		curator = []uuid.UUID{}
	}
	rows, err := tx.Query(ctx, s.sql(archive), vec, q, visible, c.Scope.Actor, curator, s.cfg.CandidatesPerMethod, s.cfg.RRFK, limit, tagFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ChunkID, &r.DocumentID, &r.SpaceID, &r.SpaceName, &r.SpaceType, &r.Title,
			&r.HeadingPath, &r.Content, &r.Status, &r.AuthorEmail, &r.UpdatedAt, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// audit records the search. The query text is only kept when no personal space was
// searched; the hit count is always kept (it feeds "searches without result" stats).
func (s *Service) audit(ctx context.Context, c Caller, q string, visible []uuid.UUID, hits int, archive bool) {
	details := map[string]any{"hits": hits}
	if archive {
		details["archive"] = true
	}
	if !includes(visible, c.PersonalSpace) {
		details["query"] = q
	}
	uid := c.Scope.Actor
	if err := audit.Insert(ctx, s.pool, audit.Event{UserID: &uid, Client: c.Client, Action: "search", Details: details}); err != nil {
		slog.Warn("audit entry for search failed", "error", err)
	}
}

func includes(ids []uuid.UUID, id *uuid.UUID) bool {
	if id == nil {
		return false
	}
	for _, x := range ids {
		if x == *id {
			return true
		}
	}
	return false
}

// restrict intersects visible with the optional filter; no filter means all visible.
func restrict(visible, filter []uuid.UUID) []uuid.UUID {
	if len(filter) == 0 {
		return visible
	}
	want := make(map[uuid.UUID]bool, len(filter))
	for _, id := range filter {
		want[id] = true
	}
	var out []uuid.UUID
	for _, id := range visible {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}
