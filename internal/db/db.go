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
}

// Open creates the connection pool and verifies connectivity.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("db verbinden: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db erreichbar? %w", err)
	}
	return pool, nil
}

// Migrate checks the stored embedding dimension, applies pending migrations and
// records the embedding setup in `meta` on first start. It refuses to run when the
// configured dimension differs from the one the database was created with.
func Migrate(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	if emb.Dimensions < 1 || emb.Dimensions > maxIndexableDimensions {
		return fmt.Errorf("embedding.dimensions %d außerhalb von 1..%d", emb.Dimensions, maxIndexableDimensions)
	}
	if err := checkDimension(ctx, pool, emb); err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(),
		goose.WithGoMigrations(chunksMigration(emb.Dimensions)))
	if err != nil {
		return fmt.Errorf("migrationen vorbereiten: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrationen ausführen: %w", err)
	}
	for _, r := range results {
		slog.Info("Migration angewendet", "version", r.Source.Version, "dauer", r.Duration)
	}

	return recordEmbedding(ctx, pool, emb)
}

// checkDimension fails when meta already holds a different dimension. On a fresh
// database (no meta table or no entry) it passes.
func checkDimension(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('meta') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("meta prüfen: %w", err)
	}
	if !exists {
		return nil
	}
	stored, ok, err := getMeta(ctx, pool, metaEmbeddingDimensions)
	if err != nil || !ok {
		return err
	}
	if stored != fmt.Sprint(emb.Dimensions) {
		return fmt.Errorf("embedding.dimensions=%d passt nicht zur Datenbank (angelegt mit %s). "+
			"Ein Modellwechsel erfordert eine Migration und admin_reembed", emb.Dimensions, stored)
	}
	return nil
}

func recordEmbedding(ctx context.Context, pool *pgxpool.Pool, emb Embedding) error {
	_, err := pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		metaEmbeddingDimensions, fmt.Sprint(emb.Dimensions))
	if err != nil {
		return fmt.Errorf("meta schreiben: %w", err)
	}
	stored, ok, err := getMeta(ctx, pool, metaEmbeddingModel)
	if err != nil {
		return err
	}
	if !ok {
		_, err = pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2)`, metaEmbeddingModel, emb.Model)
		return err
	}
	if stored != emb.Model {
		// Same dimension, different model: vectors are incomparable until re-embedded.
		slog.Warn("Embedding-Modell weicht vom gespeicherten ab – admin_reembed ausführen",
			"gespeichert", stored, "konfiguriert", emb.Model)
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
		return "", false, fmt.Errorf("meta lesen: %w", err)
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
