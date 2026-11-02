// Package docs holds the document lifecycle: store, update, archive, read, list and
// personal handoffs. Every read goes through the visibility predicate and every write
// re-checks the caller's role in the space, independent of the MCP layer.
package docs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

var (
	// ErrNotFound is returned both for missing documents/spaces and for ones the caller
	// may not see, so the two cannot be told apart.
	ErrNotFound = errors.New("nicht gefunden")
	// ErrForbidden means the caller can see the space but lacks the role for the action.
	ErrForbidden = errors.New("dazu fehlt die berechtigung in diesem bereich")
	ErrQuota     = errors.New("kontingent des persönlichen bereichs überschritten")
	ErrConflict  = errors.New("der eintrag wurde zwischenzeitlich geändert, bitte erneut laden")
	// ErrNeedsApproval: authors cannot yet change approved entries of approval spaces
	// (the draft-next-to-approved workflow arrives with phase 2).
	ErrNeedsApproval = errors.New("freigegebene einträge in bereichen mit freigabepflicht können derzeit nur kuratoren ändern")
)

type Options struct {
	PersonalMaxDocuments int
	PersonalMaxBytes     int64
	HandoffTTLDays       int
	DuplicateThreshold   float64
}

type Document struct {
	ID          uuid.UUID
	SpaceID     uuid.UUID
	Kind        string // knowledge | handoff
	Title       string
	Content     string // empty in list results
	Tags        []string
	Status      string
	ReviewNote  string
	AuthorID    uuid.UUID
	AuthorEmail string
	Source      string
	Version     int
	ExpiresAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Actor is the caller together with the roles authz computed for them.
type Actor struct {
	User  users.User
	Roles authz.Roles
	// Client names the assistant on whose behalf the call runs (audit/source).
	Client string
}

// Scope converts the actor into the visibility parameters used by queries.
func (a Actor) Scope() ingest.Scope {
	return ingest.Scope{Visible: a.Roles.Spaces(authz.Reader), Actor: a.User.ID, Curator: a.Roles.Spaces(authz.Curator)}
}

type Store struct {
	pool   *pgxpool.Pool
	spaces *spaces.Store
	ing    *ingest.Service
	opt    Options
}

func New(pool *pgxpool.Pool, sp *spaces.Store, ing *ingest.Service, opt Options) *Store {
	if opt.HandoffTTLDays < 1 {
		opt.HandoffTTLDays = 14
	}
	return &Store{pool: pool, spaces: sp, ing: ing, opt: opt}
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const docColumns = `d.id, d.space_id, d.kind, d.title, d.content, d.tags, d.status, COALESCE(d.review_note, ''),
	d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at`

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.SpaceID, &d.Kind, &d.Title, &d.Content, &d.Tags, &d.Status, &d.ReviewNote,
		&d.AuthorID, &d.AuthorEmail, &d.Source, &d.Version, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	return d, err
}

// fetch loads one document the actor may see.
func fetch(ctx context.Context, q querier, a Actor, id uuid.UUID, lock bool) (Document, error) {
	sql := `SELECT ` + docColumns + ` FROM documents d JOIN users au ON au.id = d.author_id
		WHERE d.id = $1 AND ` + visibility.Clause("d", 2, 3, 4)
	if lock {
		sql += ` FOR UPDATE OF d`
	}
	sc := a.Scope()
	return scanDoc(q.QueryRow(ctx, sql, id, nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator)))
}

// Get returns a document the actor may see.
func (s *Store) Get(ctx context.Context, a Actor, id uuid.UUID) (Document, error) {
	return fetch(ctx, s.pool, a, id, false)
}

type ListParams struct {
	SpaceID uuid.UUID
	Tag     string
	Limit   int
}

// List returns the newest knowledge documents of one space, without content.
func (s *Store) List(ctx context.Context, a Actor, p ListParams) ([]Document, error) {
	if !a.Roles.Can(p.SpaceID, authz.Reader) {
		return nil, ErrNotFound
	}
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 20
	}
	sc := a.Scope()
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.space_id, d.kind, d.title, '', d.tags, d.status, COALESCE(d.review_note, ''),
       d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at
FROM documents d JOIN users au ON au.id = d.author_id
WHERE `+visibility.Clause("d", 1, 2, 3)+`
  AND d.space_id = $4 AND d.kind = 'knowledge' AND ($5 = '' OR $5 = ANY(d.tags))
ORDER BY d.updated_at DESC LIMIT $6`,
		nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), p.SpaceID, p.Tag, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}

func collect(rows pgx.Rows) ([]Document, error) {
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
