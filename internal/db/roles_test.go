package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// scripts/db-roles.sql gives the server an account that can use the data but cannot change the
// schema, switch triggers off or alter the audit log, and a second account that migrates.
func TestHardenedAccounts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if err := Migrate(ctx, pool, Embedding{Model: "test", Dimensions: 4}); err != nil {
		t.Fatal(err)
	}
	if super, err := IsSuperuser(ctx, pool); err != nil || !super {
		t.Fatalf("the test account is expected to be a superuser: %v %v", super, err)
	}

	script, err := os.ReadFile("../../scripts/db-roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.NewReplacer(":'app_password'", "'app-pw'", ":'migrate_password'", "'migrate-pw'").Replace(string(script))
	for i := 0; i < 2; i++ { // twice: the script can be run again
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("db-roles.sql (run %d): %v", i+1, err)
		}
	}

	connect := func(user, password string) *pgxpool.Pool {
		t.Helper()
		u, err := url.Parse(os.Getenv("ATRIUM_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(user, password)
		u.Path = "/" + pool.Config().ConnConfig.Database
		p, err := pgxpool.New(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	app, migrate := connect("atrium_app", "app-pw"), connect("atrium_migrate", "migrate-pw")

	if super, err := IsSuperuser(ctx, app); err != nil || super {
		t.Fatalf("the server account is a superuser: %v %v", super, err)
	}

	// What the server needs works.
	if _, err := app.Exec(ctx, `INSERT INTO users (email, display_name, idp, idp_subject) VALUES ('a@x.de', 'A', 't', '1')`); err != nil {
		t.Fatalf("writing data: %v", err)
	}
	if _, err := app.Exec(ctx, `INSERT INTO audit_log (action) VALUES ('test')`); err != nil {
		t.Fatalf("appending to the audit log: %v", err)
	}
	var n int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reading the audit log: %d %v", n, err)
	}

	// What it must not be able to do is refused.
	for name, stmt := range map[string]string{
		"alter an audit entry":    `UPDATE audit_log SET action = 'forged'`,
		"delete an audit entry":   `DELETE FROM audit_log`,
		"truncate the audit log":  `TRUNCATE audit_log`,
		"switch triggers off":     `ALTER TABLE skill_versions DISABLE TRIGGER ALL`,
		"ignore triggers":         `SET session_replication_role = replica`,
		"change the schema":       `ALTER TABLE users ADD COLUMN extra int`,
		"create a table":          `CREATE TABLE extra (id int)`,
		"drop a table":            `DROP TABLE skill_resources`,
		"read the migration list": `SELECT * FROM goose_db_version`,
	} {
		if _, err := app.Exec(ctx, stmt); err == nil {
			t.Errorf("the server account can %s", name)
		}
	}
	if err := app.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'test'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the audit entry changed: %d %v", n, err)
	}

	// The migration account changes the schema, and what it creates is usable by the server.
	if _, err := migrate.Exec(ctx, `CREATE TABLE later_migration (id int PRIMARY KEY, note text)`); err != nil {
		t.Fatalf("migration account: %v", err)
	}
	if _, err := app.Exec(ctx, `INSERT INTO later_migration (id, note) VALUES (1, 'x')`); err != nil {
		t.Errorf("the server cannot use a table a later migration created: %v", err)
	}
}

// A new installation runs scripts/db-roles.sql on the empty database before the first start; the
// migrations then run with the migration account and must leave the audit log append-only for the
// server account.
func TestHardenedAccountsOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	script, err := os.ReadFile("../../scripts/db-roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.NewReplacer(":'app_password'", "'app-pw'", ":'migrate_password'", "'migrate-pw'").Replace(string(script))
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("db-roles.sql on an empty database: %v", err)
	}
	connect := func(user, password string) *pgxpool.Pool {
		t.Helper()
		u, err := url.Parse(os.Getenv("ATRIUM_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(user, password)
		u.Path = "/" + pool.Config().ConnConfig.Database
		p, err := pgxpool.New(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	app, migrate := connect("atrium_app", "app-pw"), connect("atrium_migrate", "migrate-pw")
	if err := Migrate(ctx, migrate, Embedding{Model: "test", Dimensions: 4}); err != nil {
		t.Fatalf("migrating with the migration account: %v", err)
	}
	if _, err := app.Exec(ctx, `INSERT INTO audit_log (action) VALUES ('test')`); err != nil {
		t.Fatalf("appending to the audit log: %v", err)
	}
	if _, err := app.Exec(ctx, `INSERT INTO users (email, display_name, idp, idp_subject) VALUES ('a@x.de', 'A', 't', '1')`); err != nil {
		t.Fatalf("writing data: %v", err)
	}
	for name, stmt := range map[string]string{
		"alter an audit entry":    `UPDATE audit_log SET action = 'forged'`,
		"delete an audit entry":   `DELETE FROM audit_log`,
		"truncate the audit log":  `TRUNCATE audit_log`,
		"switch triggers off":     `ALTER TABLE skill_versions DISABLE TRIGGER ALL`,
		"read the migration list": `SELECT * FROM goose_db_version`,
	} {
		if _, err := app.Exec(ctx, stmt); err == nil {
			t.Errorf("the server account can %s", name)
		}
	}

	// Retention goes through the purge function: old entries yes, the last 30 days never.
	var n int64
	if err := app.QueryRow(ctx, `SELECT audit_log_purge(now() - interval '7 days')`).Scan(&n); err == nil {
		t.Error("the server account purged entries younger than 30 days")
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET ts = now() - interval '100 days'`); err != nil {
		t.Fatal(err)
	}
	if err := app.QueryRow(ctx, `SELECT audit_log_purge(now() - interval '90 days')`).Scan(&n); err != nil || n != 1 {
		t.Errorf("purging old entries: %d %v", n, err)
	}
}
