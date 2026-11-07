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
	// ErrRule: binding rules are set, changed and moved by curators and admins of the space only.
	ErrRule = errors.New("binding rules (type rule) are set and changed only by curators and admins of the space: ask one of them (space_contacts)")
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
	// Type is the knowledge type: rule (binding), documentation, recommendation or experience.
	Type string
	// OwnerID is the responsible person if somebody named one (team spaces only); OwnerEmail and
	// OwnerActive describe them, a locked owner stays named until the entries are reassigned.
	OwnerID     *uuid.UUID
	OwnerEmail  string
	OwnerActive bool
	// ReviewDue is the day by which the entry should be checked, if set.
	ReviewDue *time.Time
}

// Knowledge types. Only curators and admins of a team space set or change a rule.
const (
	TypeRule           = "rule"
	TypeDocumentation  = "documentation"
	TypeRecommendation = "recommendation"
	TypeExperience     = "experience"
)

// ValidType reports whether t is a knowledge type.
func ValidType(t string) bool {
	switch t {
	case TypeRule, TypeDocumentation, TypeRecommendation, TypeExperience:
		return true
	}
	return false
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
	d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at,
	d.knowledge_type, d.owner_id, COALESCE(ou.email::text, ''), COALESCE(ou.status = 'active', false), d.review_due_at`

// docListColumns are docColumns without the content, for lists.
const docListColumns = `d.id, d.space_id, d.kind, d.title, '', d.tags, d.status, COALESCE(d.review_note, ''),
	d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at,
	d.knowledge_type, d.owner_id, COALESCE(ou.email::text, ''), COALESCE(ou.status = 'active', false), d.review_due_at`

// docFrom joins the author and the responsible person to the documents aliased d.
const docFrom = `FROM documents d JOIN users au ON au.id = d.author_id LEFT JOIN users ou ON ou.id = d.owner_id`

// docTargets are the scan targets of docColumns and docListColumns.
func docTargets(d *Document) []any {
	return []any{&d.ID, &d.SpaceID, &d.Kind, &d.Title, &d.Content, &d.Tags, &d.Status, &d.ReviewNote,
		&d.AuthorID, &d.AuthorEmail, &d.Source, &d.Version, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt,
		&d.Type, &d.OwnerID, &d.OwnerEmail, &d.OwnerActive, &d.ReviewDue}
}

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(docTargets(&d)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	return d, err
}

// fetch loads one document the actor may see.
func fetch(ctx context.Context, q querier, a Actor, id uuid.UUID, lock bool) (Document, error) {
	sql := `SELECT ` + docColumns + ` ` + docFrom + `
		WHERE d.id = $1 AND ` + visibility.Clause("d", 2, 3, 4)
	if lock {
		sql += ` FOR UPDATE OF d`
	}
	sc := a.Scope()
	return scanDoc(q.QueryRow(ctx, sql, id, nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator)))
}

// fetchAny also finds archived documents the actor may restore. Only reading, unarchiving and
// the deletion by an admin use it; every other write goes through fetch and so never touches the
// archive.
func fetchAny(ctx context.Context, q querier, a Actor, id uuid.UUID, lock bool) (Document, error) {
	sql := `SELECT ` + docColumns + ` ` + docFrom + `
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
	// Type keeps entries of one knowledge type, OwnerID those a person is responsible for, and
	// ReviewDue those whose review date has passed.
	Type      string
	OwnerID   *uuid.UUID
	ReviewDue bool
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
	if p.Type != "" && !ValidType(p.Type) {
		return nil, errors.Join(ingest.ErrInvalid, errors.New("type must be rule, documentation, recommendation or experience"))
	}
	visible := visibility.Clause("d", 1, 2, 3)
	if p.Status == "archived" {
		visible = visibility.ArchiveClause("d", 1, 2, 3)
	}
	sc := a.Scope()
	rows, err := s.pool.Query(ctx, `SELECT `+docListColumns+` `+docFrom+`
WHERE `+visible+`
  AND d.space_id = $4 AND d.kind = 'knowledge' AND ($5 = '' OR $5 = ANY(d.tags)) AND ($6 = '' OR d.status = $6)
  AND ($8 = '' OR d.knowledge_type = $8) AND ($9::uuid IS NULL OR d.owner_id = $9) AND (NOT $10 OR d.review_due_at < now())
ORDER BY d.updated_at DESC LIMIT $7`,
		nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), p.SpaceID, p.Tag, p.Status, p.Limit, p.Type, p.OwnerID, p.ReviewDue)
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
