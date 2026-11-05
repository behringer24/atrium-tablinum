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
	"github.com/behringer24/atrium-tablinum/internal/quota"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

var (
	// ErrNotFound is returned both for missing documents/spaces and for ones the caller
	// may not see, so the two cannot be told apart.
	ErrNotFound = errors.New("not found")
	// ErrForbidden means the caller can see the space but lacks the role for the action.
	ErrForbidden = errors.New("you do not have permission for this in this space")
	ErrQuota     = errors.New("quota exceeded")
	ErrConflict  = errors.New("the entry was changed in the meantime, please reload it")
	// ErrNeedsApproval: authors cannot yet change approved entries of approval spaces
	// (a draft next to the approved version is not implemented).
	ErrNeedsApproval = errors.New("approved entries in spaces that require approval can currently only be changed by curators")
	// ErrOwnDraft: curators cannot release their own draft, admins can.
	ErrOwnDraft = errors.New("only a space admin can approve their own draft, otherwise another curator has to do it")
)

type Options struct {
	// Quota resolves the storage limits of a space (runtime settings over config defaults);
	// nil = no limits.
	Quota              *quota.Store
	HandoffTTLDays     int
	DuplicateThreshold float64
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

// fetchAny also finds archived documents the actor may restore. Only reading and
// unarchiving use it; every other write goes through fetch and so never touches the archive.
func fetchAny(ctx context.Context, q querier, a Actor, id uuid.UUID, lock bool) (Document, error) {
	sql := `SELECT ` + docColumns + ` FROM documents d JOIN users au ON au.id = d.author_id
		WHERE d.id = $1 AND (` + visibility.Clause("d", 2, 3, 4) + ` OR ` + visibility.ArchiveClause("d", 2, 3, 4) + `)`
	if lock {
		sql += ` FOR UPDATE OF d`
	}
	sc := a.Scope()
	return scanDoc(q.QueryRow(ctx, sql, id, nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator)))
}

// Get returns a document the actor may see, including archived ones they may restore.
func (s *Store) Get(ctx context.Context, a Actor, id uuid.UUID) (Document, error) {
	return fetchAny(ctx, s.pool, a, id, false)
}

type ListParams struct {
	SpaceID uuid.UUID
	Tag     string
	// Status limits the list to draft, approved, rejected or archived entries (empty: all
	// active ones the actor may see). Drafts and rejected entries are visible only to their
	// author and curators; archived ones only to those who may restore them.
	Status string
	Limit  int
}

// List returns the newest knowledge documents of one space, without content.
func (s *Store) List(ctx context.Context, a Actor, p ListParams) ([]Document, error) {
	if !a.Roles.Can(p.SpaceID, authz.Reader) {
		return nil, ErrNotFound
	}
	switch p.Status {
	case "", "draft", "approved", "rejected", "archived":
	default:
		return nil, errors.Join(ingest.ErrInvalid, errors.New("status must be draft, approved, rejected or archived"))
	}
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 20
	}
	visible := visibility.Clause("d", 1, 2, 3)
	if p.Status == "archived" {
		visible = visibility.ArchiveClause("d", 1, 2, 3)
	}
	sc := a.Scope()
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.space_id, d.kind, d.title, '', d.tags, d.status, COALESCE(d.review_note, ''),
       d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at
FROM documents d JOIN users au ON au.id = d.author_id
WHERE `+visible+`
  AND d.space_id = $4 AND d.kind = 'knowledge' AND ($5 = '' OR $5 = ANY(d.tags)) AND ($6 = '' OR d.status = $6)
ORDER BY d.updated_at DESC LIMIT $7`,
		nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), p.SpaceID, p.Tag, p.Status, p.Limit)
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
