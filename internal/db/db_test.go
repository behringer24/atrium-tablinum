package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool returns a pool on a fresh, empty database. Set ATRIUM_TEST_DATABASE_URL to a
// superuser-ish URL (e.g. the dev compose: postgres://knowledge:knowledge@localhost:5432/knowledge).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("ATRIUM_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("ATRIUM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "atrium_test_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(base)
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
		admin.Close()
	})
	return pool
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	emb := Embedding{Model: "BAAI/bge-m3", Dimensions: 1024}

	if err := Migrate(ctx, pool, emb); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool, emb); err != nil {
		t.Fatalf("second start: %v", err)
	}
	for _, tbl := range []string{"users", "spaces", "documents", "chunks", "audit_log", "jobs"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&ok); err != nil || !ok {
			t.Errorf("table %s missing (%v)", tbl, err)
		}
	}
	if v, ok, _ := getMeta(ctx, pool, metaEmbeddingDimensions); !ok || v != "1024" {
		t.Errorf("meta dimensions = %q", v)
	}
}

func TestMigrateRejectsDimensionChange(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool, Embedding{Model: "m", Dimensions: 1024}); err != nil {
		t.Fatal(err)
	}
	err := Migrate(ctx, pool, Embedding{Model: "m", Dimensions: 768})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
}

// With allow_dimension_change one start rebuilds the vector column: vectors go, contents stay.
func TestMigrateChangesDimensionOnRequest(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool, Embedding{Model: "old", Dimensions: 4}); err != nil {
		t.Fatal(err)
	}
	var doc string
	err := pool.QueryRow(ctx, `
WITH u AS (INSERT INTO users (email, idp, idp_subject) VALUES ('a@x.de', 't', 'a') RETURNING id),
     s AS (INSERT INTO spaces (slug, name, type) VALUES ('dev', 'Dev', 'team') RETURNING id)
INSERT INTO documents (space_id, title, content, author_id, source, updated_by)
SELECT s.id, 'T', 'text', u.id, 'api', u.id FROM u, s RETURNING id`).Scan(&doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chunks (document_id, chunk_index, content, embedding, embedding_model, tsv)
		VALUES ($1, 0, 'text', '[1,0,0,0]', 'old', to_tsvector('simple', 'text'))`, doc); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, pool, Embedding{Model: "new", Dimensions: 3, AllowDimensionChange: true}); err != nil {
		t.Fatal(err)
	}
	var typ, model, content string
	var empty bool
	if err := pool.QueryRow(ctx, `SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a
		WHERE a.attrelid = 'chunks'::regclass AND a.attname = 'embedding'`).Scan(&typ); err != nil || typ != "vector(3)" {
		t.Errorf("column type %q %v", typ, err)
	}
	if err := pool.QueryRow(ctx, `SELECT embedding IS NULL, embedding_model, content FROM chunks`).Scan(&empty, &model, &content); err != nil ||
		!empty || model != "" || content != "text" {
		t.Errorf("section after the change: vector gone %v, model %q, content %q, %v", empty, model, content, err)
	}
	if v, _, _ := getMeta(ctx, pool, metaEmbeddingDimensions); v != "3" {
		t.Errorf("meta dimensions %q", v)
	}
	var idx bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('chunks_embedding_idx') IS NOT NULL`).Scan(&idx); err != nil || !idx {
		t.Errorf("vector index missing: %v", err)
	}
	// a further start with the same setting changes nothing
	if err := Migrate(ctx, pool, Embedding{Model: "new", Dimensions: 3, AllowDimensionChange: true}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateRejectsInvalidDimension(t *testing.T) {
	pool := testPool(t)
	if err := Migrate(context.Background(), pool, Embedding{Dimensions: 4000}); err == nil {
		t.Fatal("4000 dimensions must be rejected")
	}
}

func TestSchemaConstraints(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool, Embedding{Model: "m", Dimensions: 4}); err != nil {
		t.Fatal(err)
	}
	var uid, gid, sid string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (email, idp, idp_subject) VALUES ('a@x.de','g','1') RETURNING id`).Scan(&uid))
	must(pool.QueryRow(ctx, `INSERT INTO groups (name, source) VALUES ('g1','local') RETURNING id`).Scan(&gid))
	must(pool.QueryRow(ctx, `INSERT INTO spaces (slug,name,type) VALUES ('t','T','team') RETURNING id`).Scan(&sid))

	// personal space needs an owner; team must not have one
	if _, err := pool.Exec(ctx, `INSERT INTO spaces (slug,name,type) VALUES ('p','P','personal')`); err == nil {
		t.Error("personal without owner must fail")
	}
	// only one personal space per user
	_, err := pool.Exec(ctx, `INSERT INTO spaces (slug,name,type,owner_user_id) VALUES ('p1','P','personal',$1)`, uid)
	must(err)
	if _, err := pool.Exec(ctx, `INSERT INTO spaces (slug,name,type,owner_user_id) VALUES ('p2','P','personal',$1)`, uid); err == nil {
		t.Error("second personal space must fail")
	}
	// handoff needs expires_at
	if _, err := pool.Exec(ctx, `INSERT INTO documents (space_id,kind,title,content,author_id,source,updated_by)
		VALUES ($1,'handoff','t','c',$2,'claude',$2)`, sid, uid); err == nil {
		t.Error("handoff without expires_at must fail")
	}
	// memberships disappear with their principal
	_, err = pool.Exec(ctx, `INSERT INTO space_members (space_id,principal_type,principal_id,role) VALUES ($1,'group',$2,'reader'),($1,'user',$3,'author')`, sid, gid, uid)
	must(err)
	_, err = pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, gid)
	must(err)
	var n int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM space_members WHERE principal_id=$1`, gid).Scan(&n))
	if n != 0 {
		t.Errorf("group membership remained (%d)", n)
	}
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid)
	must(err)
	must(pool.QueryRow(ctx, `SELECT count(*) FROM space_members WHERE principal_id=$1`, uid).Scan(&n))
	if n != 0 {
		t.Errorf("user membership remained (%d)", n)
	}
}
