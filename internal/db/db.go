// Package db owns the Postgres connection pool, schema migrations and the
// start-up check that the configured embedding model matches the stored data.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	metaEmbeddingDimensions = "embedding_dimensions"
	metaEmbeddingModel      = "embedding_model"

	// pgvector can index at most 2000 dimensions with HNSW on the vector type.
	maxIndexableDimensions = 2000
)

// Embedding describes the embedding setup the server is configured with.
type Embedding struct {
	Model      string
	Dimensions int
	// AllowDimensionChange lets a start with a dimension other than the stored one rebuild the vector
	// column: every vector is dropped and the re-embedding task (admin_reembed) builds them again.
	AllowDimensionChange bool
}

// Open creates the connection pool and verifies connectivity.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connecting to db: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db reachable? %w", err)
	}
	return pool, nil
}

// Migrate checks the stored embedding dimension, applies pending migrations and
// records the embedding setup in `meta` on first start. It refuses to run when the
// configured dimension differs from the one the database was created with.
func Migrate(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	if emb.Dimensions < 1 || emb.Dimensions > maxIndexableDimensions {
		return fmt.Errorf("embedding.dimensions %d out of range 1..%d", emb.Dimensions, maxIndexableDimensions)
	}
	change, err := checkDimension(ctx, pool, emb)
	if err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(),
		goose.WithGoMigrations(chunksMigration(emb.Dimensions)))
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	for _, r := range results {
		slog.Info("Migration applied", "version", r.Source.Version, "duration", r.Duration)
	}
	if change {
		if err := changeDimension(ctx, pool, emb); err != nil {
			return err
		}
	}
	return recordEmbedding(ctx, pool, emb)
}

// checkDimension compares the configured dimension with the one stored in meta. A different one
// fails, unless the configuration allows the change; then it reports that the vector column has to
// be rebuilt. On a fresh database (no meta table or no entry) it passes.
func checkDimension(ctx context.Context, pool *pgxpool.Pool, emb Embedding) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('meta') IS NOT NULL`).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking meta: %w", err)
	}
	if !exists {
		return false, nil
	}
	stored, ok, err := getMeta(ctx, pool, metaEmbeddingDimensions)
	if err != nil || !ok {
		return false, err
	}
	if stored == fmt.Sprint(emb.Dimensions) {
		return false, nil
	}
	if !emb.AllowDimensionChange {
		return false, fmt.Errorf("embedding.dimensions=%d does not match the database (created with %s). "+
			"To switch to a model with another dimension, set embedding.allow_dimension_change: true for one start: "+
			"all vectors are dropped and rebuilt with admin_reembed, full-text search keeps working meanwhile (docs/embedding.md)",
			emb.Dimensions, stored)
	}
	return true, nil
}

// changeDimension rebuilds the vector column for a new dimension. Every section loses its vector and
// is marked as not embedded (an empty embedding_model), so search finds it by full text until the
// re-embedding task has rebuilt it; the contents and everything else stay.
func changeDimension(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DROP INDEX IF EXISTS chunks_embedding_idx`,
		fmt.Sprintf(`ALTER TABLE chunks ALTER COLUMN embedding TYPE vector(%d) USING NULL`, emb.Dimensions),
		`UPDATE chunks SET embedding_model = ''`,
		`CREATE INDEX chunks_embedding_idx ON chunks USING hnsw (embedding vector_cosine_ops)`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("changing the embedding dimension: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE meta SET value = $2 WHERE key = $1`, metaEmbeddingDimensions, fmt.Sprint(emb.Dimensions)); err != nil {
		return err
	}
	slog.Warn("Embedding dimension changed: all vectors were dropped. Start re-embedding with admin_reembed; "+
		"until then search uses full text only. Remove embedding.allow_dimension_change from the configuration.",
		"dimensions", emb.Dimensions)
	return tx.Commit(ctx)
}

func recordEmbedding(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	_, err := pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		metaEmbeddingDimensions, fmt.Sprint(emb.Dimensions))
	if err != nil {
		return fmt.Errorf("writing meta: %w", err)
	}
	// The model in meta is the one configured at the last start; which model built a vector is
	// recorded per section (chunks.embedding_model), and only those decide what is stale.
	if _, err := pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		metaEmbeddingModel, emb.Model); err != nil {
		return fmt.Errorf("writing meta: %w", err)
	}
	var stale int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE embedding IS NULL OR embedding_model <> $1`, emb.Model).Scan(&stale); err != nil {
		return fmt.Errorf("counting sections: %w", err)
	}
	if stale > 0 {
		// Vectors of different models are not comparable: search ignores them until re-embedded.
		slog.Warn("Sections are not embedded with the configured model and are found by full text only - run admin_reembed",
			"sections", stale, "model", emb.Model)
	}
	return nil
}

func getMeta(ctx context.Context, pool *pgxpool.Pool, key string) (string, bool, error) {
	var v string
	err := pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading meta: %w", err)
	}
	return v, true, nil
}

// chunksMigration creates `chunks` with the configured vector dimension.
func chunksMigration(dim int) *goose.Migration {
	up := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE chunks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     uuid         NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index     int          NOT NULL,
    heading_path    text         NOT NULL DEFAULT '',
    content         text         NOT NULL,
    embedding       vector(%d)   NOT NULL,
    embedding_model text         NOT NULL,
    tsv             tsvector     NOT NULL,
    UNIQUE (document_id, chunk_index)
);
CREATE INDEX chunks_embedding_idx ON chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX chunks_tsv_idx       ON chunks USING GIN (tsv);
CREATE INDEX chunks_document_idx  ON chunks (document_id);`, dim))
		return err
	}
	down := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE chunks`)
		return err
	}
	return goose.NewGoMigration(2, &goose.GoFunc{RunTx: up}, &goose.GoFunc{RunTx: down})
}

func mustSub() fs.FS {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

// IsSuperuser reports whether the connected account is a database superuser, which can switch off
// triggers and alter anything, the audit log included.
func IsSuperuser(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var super bool
	err := pool.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super)
	return super, err
}
