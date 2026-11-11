// Package reembed rebuilds the vectors of sections that were embedded with another model than the
// configured one, or lost theirs after a change of dimension. Vectors of different models cannot be
// compared, so search and the duplicate check use only those of the configured model; the others
// are found by full text until they are rebuilt.
//
// An instance admin requests the rebuild (admin_reembed); the request is kept in meta, and a
// periodic task works through the stale sections batch by batch. A restart only pauses it. Each
// section is embedded from its stored text exactly as when an entry is saved.
package reembed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
)

// metaKey holds the time a rebuild was requested; no row means none is wanted.
const metaKey = "reembed_requested"

// Defaults of a Service.
const (
	DefaultBatch  = 16
	DefaultBudget = 50 * time.Second
	// DefaultPendingBatch is how many sections IndexPending sends at once. The embedding service
	// works through a request before it takes the next, so a search that arrives meanwhile waits for it:
	// 4 sections keep that to about ten seconds on two cores, where 16 would make it forty.
	DefaultPendingBatch = 4
)

type Service struct {
	pool *pgxpool.Pool
	emb  embed.Embedder
	// Batch is the number of sections sent to the embedder at once; Budget how long one run of the
	// periodic task works before it hands over to the next run.
	Batch  int
	Budget time.Duration
	// PendingBatch is the batch size of IndexPending, which runs all the time beside searches and
	// saves; a rebuild (Run) is requested and may take the larger Batch.
	PendingBatch int
}

func New(pool *pgxpool.Pool, emb embed.Embedder) *Service {
	return &Service{pool: pool, emb: emb, Batch: DefaultBatch, Budget: DefaultBudget, PendingBatch: DefaultPendingBatch}
}

// Status describes the vectors of all sections.
type Status struct {
	Model string // the configured model
	Total int
	// Stale counts the sections embedded with another model or without a vector.
	Stale int
	// Pending counts the sections of new entries that wait for their first vector; the background task
	// embeds them by itself (IndexPending) and they are not part of Stale.
	Pending int
	// ByModel counts the sections per model that built their vector; "" stands for none.
	ByModel   map[string]int
	Requested *time.Time
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{Model: s.emb.Model(), ByModel: map[string]int{}}
	rows, err := s.pool.Query(ctx, `SELECT CASE WHEN embedding IS NULL AND embedding_model <> $1 THEN '' ELSE embedding_model END, count(*)
		FROM chunks GROUP BY 1`, ingest.PendingModel)
	if err != nil {
		return Status{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var n int
		if err := rows.Scan(&model, &n); err != nil {
			return Status{}, err
		}
		st.Total += n
		if model == ingest.PendingModel {
			st.Pending = n
			continue
		}
		st.ByModel[model] = n
		if model != st.Model {
			st.Stale += n
		}
	}
	if err := rows.Err(); err != nil {
		return Status{}, err
	}
	if st.Requested, err = s.requested(ctx); err != nil {
		return Status{}, err
	}
	return st, nil
}

func (s *Service) requested(ctx context.Context) (*time.Time, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, metaKey).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("reading the re-embedding request: %w", err)
	}
	return &t, nil
}

// Request asks the periodic task to rebuild every stale section. Asking again keeps the first time.
func (s *Service) Request(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		metaKey, time.Now().UTC().Format(time.RFC3339))
	return err
}

// Cancel withdraws a request; what was rebuilt stays. It reports whether there was one.
func (s *Service) Cancel(ctx context.Context) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, metaKey)
	return tag.RowsAffected() > 0, err
}

// Run is the periodic task. While a rebuild is requested it embeds stale sections in batches until
// none is left, then withdraws the request; after Budget it stops and the next run goes on. An
// embedder error ends the run and is reported as the task's error; the next run tries again.
func (s *Service) Run(ctx context.Context) error {
	since, err := s.requested(ctx)
	if err != nil || since == nil {
		return err
	}
	deadline := time.Now().Add(s.Budget)
	after := uuid.Nil
	for time.Now().Before(deadline) {
		n, last, err := s.batch(ctx, after, false)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		after = last
	}
	var stale int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE embedding IS NULL OR embedding_model <> $1`, s.emb.Model()).Scan(&stale); err != nil {
		return err
	}
	if stale > 0 {
		slog.Info("Re-embedding continues", "sections_left", stale, "model", s.emb.Model())
		return nil
	}
	if _, err := s.Cancel(ctx); err != nil {
		return err
	}
	slog.Info("Re-embedding finished: every section is embedded with the configured model", "model", s.emb.Model())
	return nil
}

// IndexPending is the periodic task that embeds the sections of new entries: a long entry is saved
// with its first sections embedded and the rest waiting (ingest.PrepareUpTo), so that saving answers
// at once instead of after minutes of embedding. Unlike Run it needs no request: this is not a
// rebuild but the second half of saving. After Budget it stops and the next run goes on; an embedder
// error ends the run and is reported as the task's error, the next run tries again. Until a section
// has its vector it is found by full text.
func (s *Service) IndexPending(ctx context.Context) error {
	deadline := time.Now().Add(s.Budget)
	after := uuid.Nil
	done := 0
	for time.Now().Before(deadline) {
		n, last, err := s.batch(ctx, after, true)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		done += n
		after = last
	}
	if done > 0 {
		slog.Info("Sections of new entries embedded in the background", "sections", done, "model", s.emb.Model())
	}
	return nil
}

// batch rebuilds the next stale sections after the given id (in id order, so that one run passes
// over the table once) and returns how many it did and the last id. With pendingOnly it takes only
// the sections that wait for their first vector.
func (s *Service) batch(ctx context.Context, after uuid.UUID, pendingOnly bool) (int, uuid.UUID, error) {
	model := s.emb.Model()
	cond, arg, limit := `(embedding IS NULL OR embedding_model <> $2)`, model, s.Batch
	if pendingOnly {
		cond, arg, limit = `(embedding IS NULL AND embedding_model = $2)`, ingest.PendingModel, s.PendingBatch
	}
	rows, err := s.pool.Query(ctx, `SELECT id, content FROM chunks
		WHERE id > $1 AND `+cond+` ORDER BY id LIMIT $3`, after, arg, max(limit, 1))
	if err != nil {
		return 0, after, err
	}
	var ids []uuid.UUID
	var texts []string
	for rows.Next() {
		var id uuid.UUID
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return 0, after, err
		}
		ids, texts = append(ids, id), append(texts, text)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return 0, after, err
	}
	vecs, err := s.emb.Embed(ctx, texts)
	if err != nil {
		return 0, after, fmt.Errorf("embedding for the rebuild: %w", err)
	}
	if len(vecs) != len(texts) {
		return 0, after, fmt.Errorf("the embedder returned %d vectors for %d sections", len(vecs), len(texts))
	}
	batch := &pgx.Batch{}
	for i, id := range ids {
		if len(vecs[i]) != s.emb.Dimensions() {
			return 0, after, fmt.Errorf("the embedder returned %d dimensions, the configuration says %d", len(vecs[i]), s.emb.Dimensions())
		}
		// A section replaced in the meantime has a new id; the old one is gone and nothing happens.
		batch.Queue(`UPDATE chunks SET embedding = $2::vector, embedding_model = $3 WHERE id = $1`, id, ingest.VectorLiteral(vecs[i]), model)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, after, err
	}
	return len(ids), ids[len(ids)-1], nil
}
