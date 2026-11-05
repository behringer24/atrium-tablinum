// Package dbtest gives integration tests a fresh, fully migrated database.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/db"
)

// Dimensions is the vector size used by test databases.
const Dimensions = 4

// NewPool returns a pool on a new migrated database that is dropped after the test.
// Set ATRIUM_TEST_DATABASE_URL (e.g. postgres://knowledge:knowledge@localhost:5432/knowledge);
// without it the test is skipped.
func NewPool(t *testing.T) *pgxpool.Pool {
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
	name := "atrium_test_" + strings.NewReplacer("/", "_", " ", "_").Replace(strings.ToLower(t.Name()))
	if len(name) > 60 {
		name = name[:60]
	}
	_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(base)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := db.Migrate(ctx, pool, db.Embedding{Model: "test", Dimensions: Dimensions}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ATRIUM_TEST_RESTRICTED") != "" {
		return restricted(t, pool)
	}
	return pool
}

// restricted applies scripts/db-roles.sql to the test database and returns a pool that connects as
// the server account of the hardened setup, to check that the code needs no more than that account
// has. Tests that tamper with the schema on purpose (switching triggers off) fail in this mode.
func restricted(t *testing.T, super *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "scripts", "db-roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.NewReplacer(":'app_password'", "'app-pw'", ":'migrate_password'", "'migrate-pw'").Replace(string(script))
	if _, err := super.Exec(ctx, sql); err != nil {
		t.Fatalf("db-roles.sql: %v", err)
	}
	u, err := url.Parse(os.Getenv("ATRIUM_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("atrium_app", "app-pw")
	u.Path = "/" + super.Config().ConnConfig.Database
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
