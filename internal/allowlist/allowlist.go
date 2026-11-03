// Package allowlist stores the login allowlist entries an instance admin maintains at
// runtime. They add to the allowed_domains, allowed_emails and allowed_tenants of the
// configuration; the idp package merges both at every login.
package allowlist

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/idp"
)

const (
	KindDomain = "domain"
	KindEmail  = "email"
	KindTenant = "tenant"

	maxEntries = 1000
)

var (
	ErrInvalid  = errors.New("invalid entry")
	ErrTooMany  = errors.New("too many entries")
	ErrNotFound = errors.New("entry not found")

	domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

type Entry struct {
	Kind      string
	Value     string
	Provider  string // "" = every provider of the matching type
	CreatedAt time.Time
	CreatedBy string // e-mail of the admin, "" if the account is gone
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Normalize checks and canonicalizes an entry: lower case, a domain without "@" or wildcard,
// a single plain address, a tenant as GUID.
func Normalize(kind, value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch kind {
	case KindDomain:
		if !domainRe.MatchString(v) {
			return "", fmt.Errorf("%w: %q is not a domain (without @ and without *, e.g. example.com)", ErrInvalid, value)
		}
	case KindEmail:
		a, err := mail.ParseAddress(v)
		if err != nil || a.Address != v || a.Name != "" || strings.Count(v, "@") != 1 {
			return "", fmt.Errorf("%w: %q is not a single e-mail address", ErrInvalid, value)
		}
	case KindTenant:
		id, err := uuid.Parse(v)
		if err != nil {
			return "", fmt.Errorf("%w: %q is not a tenant ID (GUID)", ErrInvalid, value)
		}
		v = id.String()
	default:
		return "", fmt.Errorf("%w: kind must be domain, email or tenant", ErrInvalid)
	}
	return v, nil
}

// Add stores an entry; adding an existing one changes nothing. It reports whether the entry is new.
func (s *Store) Add(ctx context.Context, kind, value, provider string, by uuid.UUID) (added bool, err error) {
	v, err := Normalize(kind, value)
	if err != nil {
		return false, err
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM login_allowlist`).Scan(&n); err != nil {
		return false, err
	}
	if n >= maxEntries {
		return false, ErrTooMany
	}
	var creator *uuid.UUID
	if by != uuid.Nil {
		creator = &by
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO login_allowlist (kind, value, provider, created_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, kind, v, provider, creator)
	return tag.RowsAffected() == 1, err
}

func (s *Store) Remove(ctx context.Context, kind, value, provider string) error {
	v, err := Normalize(kind, value)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM login_allowlist WHERE kind = $1 AND value = $2 AND provider = $3`, kind, v, provider)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.kind, a.value, a.provider, a.created_at, COALESCE(u.email::text, '')
		FROM login_allowlist a LEFT JOIN users u ON u.id = a.created_by ORDER BY a.kind, a.value, a.provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Kind, &e.Value, &e.Provider, &e.CreatedAt, &e.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Additions implements idp.Additions: the entries that apply to one provider.
func (s *Store) Additions(ctx context.Context, providerID string) (idp.Entries, error) {
	rows, err := s.pool.Query(ctx, `SELECT kind, value FROM login_allowlist WHERE provider IN ('', $1)`, providerID)
	if err != nil {
		return idp.Entries{}, err
	}
	defer rows.Close()
	var e idp.Entries
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return idp.Entries{}, err
		}
		switch kind {
		case KindDomain:
			e.Domains = append(e.Domains, value)
		case KindEmail:
			e.Emails = append(e.Emails, value)
		case KindTenant:
			e.Tenants = append(e.Tenants, value)
		}
	}
	return e, rows.Err()
}
