package db

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Migration 00018: service accounts, their tokens, and the keys of entries that a tool keeps up to date.
func TestServiceAccountSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool, Embedding{Model: "m", Dimensions: 4}); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	refused := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	var person, svc, space, space2 string
	must(pool.QueryRow(ctx, `INSERT INTO users (email, idp, idp_subject) VALUES ('a@x.de','g','1') RETURNING id`).Scan(&person))
	must(pool.QueryRow(ctx, `INSERT INTO users (email, idp, idp_subject, kind) VALUES ('docs@service.invalid','service','docs','service') RETURNING id`).Scan(&svc))
	must(pool.QueryRow(ctx, `INSERT INTO spaces (slug,name,type) VALUES ('t','T','team') RETURNING id`).Scan(&space))
	must(pool.QueryRow(ctx, `INSERT INTO spaces (slug,name,type) VALUES ('u','U','team') RETURNING id`).Scan(&space2))

	// kind
	var kind string
	must(pool.QueryRow(ctx, `SELECT kind FROM users WHERE id = $1`, person).Scan(&kind))
	if kind != "person" {
		t.Errorf("default kind = %q", kind)
	}
	_, err := pool.Exec(ctx, `INSERT INTO users (email, idp, idp_subject, kind) VALUES ('x@x.de','g','2','robot')`)
	refused("an unknown kind", err)
	_, err = pool.Exec(ctx, `UPDATE users SET instance_admin = true WHERE id = $1`, svc)
	refused("an admin service account", err)
	_, err = pool.Exec(ctx, `UPDATE users SET instance_admin = true WHERE id = $1`, person)
	must(err)

	// the person who answers for a service account
	_, err = pool.Exec(ctx, `UPDATE users SET responsible_id = $2 WHERE id = $1`, svc, person)
	must(err)
	_, err = pool.Exec(ctx, `UPDATE users SET responsible_id = $2 WHERE id = $1`, person, svc)
	refused("a responsible person for a person", err)
	_, err = pool.Exec(ctx, `UPDATE users SET responsible_id = id WHERE id = $1`, svc)
	refused("a service account that answers for itself", err)

	// tokens
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'first', decode($2, 'hex'), now() + interval '90 days')`, svc, strings.Repeat("aa", 32))
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'short', decode($2, 'hex'), now() + interval '90 days')`, svc, strings.Repeat("bb", 8))
	refused("a token hash of the wrong length", err)
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'same-hash', decode($2, 'hex'), now() + interval '90 days')`, svc, strings.Repeat("aa", 32))
	refused("a second token with the same hash", err)
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'first', decode($2, 'hex'), now() + interval '90 days')`, svc, strings.Repeat("cc", 32))
	refused("a second token with the same name for one account", err)
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'never', decode($2, 'hex'), NULL)`, svc, strings.Repeat("dd", 32))
	refused("a token without an expiry", err)
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, created_at, expires_at)
		VALUES ($1, 'past', decode($2, 'hex'), now(), now() - interval '1 day')`, svc, strings.Repeat("ee", 32))
	refused("a token that expires before it was created", err)
	// a rotation: a second live token with another name
	_, err = pool.Exec(ctx, `INSERT INTO service_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, 'second', decode($2, 'hex'), now() + interval '90 days')`, svc, strings.Repeat("ff", 32))
	must(err)

	// entries with a key
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by,sync_key)
		VALUES ($1,'t','c',$2,'sync',$2,'git:repo:a.md')`, space, svc)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by,sync_key)
		VALUES ($1,'t2','c2',$2,'sync',$2,'git:repo:a.md')`, space, svc)
	refused("the same key twice in one space", err)
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by,sync_key)
		VALUES ($1,'t3','c3',$2,'sync',$2,'git:repo:a.md')`, space2, svc)
	must(err) // another space may use the same key
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by)
		VALUES ($1,'plain','c',$2,'claude',$2),($1,'plain','c',$2,'claude',$2)`, space, person)
	must(err) // entries without a key are not constrained
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,kind,title,content,author_id,source,updated_by,expires_at,sync_key)
		VALUES ($1,'handoff','h','c',$2,'claude',$2,now() + interval '1 day','git:repo:h.md')`, space, person)
	refused("a key on a handoff", err)
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by,managed_by)
		VALUES ($1,'m','c',$2,'sync',$2,$2)`, space, svc)
	refused("an owner without a key", err)
	_, err = pool.Exec(ctx, `INSERT INTO documents (space_id,title,content,author_id,source,updated_by,sync_key,managed_by)
		VALUES ($1,'m','c',$2,'sync',$2,'git:repo:m.md',$2)`, space, svc)
	must(err)

	// Tokens go with their account; an account that owns entries cannot be removed from under them.
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, svc)
	refused("removing an account that owns entries", err)
	_, err = pool.Exec(ctx, `UPDATE documents SET sync_key = NULL, managed_by = NULL WHERE managed_by = $1 OR sync_key IS NOT NULL`, svc)
	must(err)
	_, err = pool.Exec(ctx, `DELETE FROM documents WHERE author_id = $1`, svc)
	must(err)
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, svc)
	must(err)
	var n int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM service_tokens`).Scan(&n))
	if n != 0 {
		t.Errorf("%d tokens remained after the account was removed", n)
	}
}

// The migration can be undone and applied again.
func TestServiceAccountMigrationDownAndUp(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool, Embedding{Model: "m", Dimensions: 4}); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile("migrations/00018_service_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(file), "-- +goose Down")
	if !ok {
		t.Fatal("no Down part")
	}
	up = strings.Replace(up, "-- +goose Up", "", 1)
	exists := func(q string) bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	const kindColumn = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'kind'`
	const responsibleColumn = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'responsible_id'`
	const tokenTable = `SELECT count(*) FROM information_schema.tables WHERE table_name = 'service_tokens'`
	const syncColumn = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'documents' AND column_name IN ('sync_key', 'managed_by')`
	if !exists(kindColumn) || !exists(responsibleColumn) || !exists(tokenTable) || !exists(syncColumn) {
		t.Fatal("the migration did not apply")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if exists(kindColumn) || exists(responsibleColumn) || exists(tokenTable) || exists(syncColumn) {
		t.Error("the Down part left something behind")
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if !exists(kindColumn) || !exists(responsibleColumn) || !exists(tokenTable) || !exists(syncColumn) {
		t.Error("the migration did not apply again")
	}
}
