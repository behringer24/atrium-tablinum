// Package spaces manages team spaces and their memberships. Methods here do not check
// the caller's permissions; the MCP layer and the domain packages do that with authz.
package spaces

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrSlugTaken = errors.New("slug is already taken")
	ErrInvalid   = errors.New("invalid input")
	ErrNotTeam   = errors.New("team spaces only")
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type Space struct {
	ID              uuid.UUID
	Slug            string
	Name            string
	Description     string
	Type            string // personal | team
	OwnerUserID     *uuid.UUID
	RequireApproval bool
	DefaultRole     authz.Role // None = no access for non-members
	Archived        bool
}

type Store struct {
	pool *pgxpool.Pool

	// OnChange is called after changes that can alter effective roles. Wire it to
	// authz.Checker.InvalidateAll.
	OnChange func()
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

const spaceSelect = `
SELECT id, slug, name, description, type, owner_user_id, require_approval,
       COALESCE(default_role, ''), status = 'archived'
FROM spaces `

func scan(row pgx.Row) (Space, error) {
	var (
		sp  Space
		def string
	)
	err := row.Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.Description, &sp.Type, &sp.OwnerUserID,
		&sp.RequireApproval, &def, &sp.Archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	sp.DefaultRole, _ = authz.ParseRole(def)
	return sp, err
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Space, error) {
	return scan(s.pool.QueryRow(ctx, spaceSelect+`WHERE id = $1`, id))
}

func (s *Store) GetBySlug(ctx context.Context, slug string) (Space, error) {
	return scan(s.pool.QueryRow(ctx, spaceSelect+`WHERE slug = $1`, strings.ToLower(strings.TrimSpace(slug))))
}

// ListByIDs returns the given spaces (used with the IDs from authz.Roles) ordered by name.
func (s *Store) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]Space, error) {
	rows, err := s.pool.Query(ctx, spaceSelect+`WHERE id = ANY($1::uuid[]) ORDER BY type DESC, name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		sp, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

type CreateParams struct {
	Slug            string
	Name            string
	Description     string
	RequireApproval bool
	DefaultRole     authz.Role // None or Reader or Author
}

// validate normalizes the slug and checks the fields of a new team space.
func (p *CreateParams) validate() error {
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	if !slugRe.MatchString(p.Slug) || strings.HasPrefix(p.Slug, "personal-") {
		return errors.Join(ErrInvalid, errors.New("slug: 2-63 characters from a-z, 0-9, '-'; prefix 'personal-' is reserved"))
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.Join(ErrInvalid, errors.New("name is missing"))
	}
	if p.DefaultRole != authz.None && p.DefaultRole != authz.Reader && p.DefaultRole != authz.Author {
		return errors.Join(ErrInvalid, errors.New("default_role: reader or author only"))
	}
	return nil
}

func (s *Store) CreateTeam(ctx context.Context, p CreateParams) (Space, error) {
	if err := p.validate(); err != nil {
		return Space{}, err
	}
	sp, err := scan(s.pool.QueryRow(ctx, `
INSERT INTO spaces (slug, name, description, type, require_approval, default_role)
VALUES ($1, $2, $3, 'team', $4, NULLIF($5, ''))
RETURNING id, slug, name, description, type, owner_user_id, require_approval, COALESCE(default_role, ''), status = 'archived'`,
		p.Slug, strings.TrimSpace(p.Name), p.Description, p.RequireApproval, p.DefaultRole.String()))
	if isUnique(err) {
		return Space{}, ErrSlugTaken
	}
	if err == nil {
		s.changed()
	}
	return sp, err
}

type UpdateParams struct {
	Name            *string
	Description     *string
	RequireApproval *bool
	// DefaultRole: nil = unchanged; pointer to authz.None clears it.
	DefaultRole *authz.Role
}

func (s *Store) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (Space, error) {
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		return Space{}, errors.Join(ErrInvalid, errors.New("name is missing"))
	}
	setDef, def := false, ""
	if p.DefaultRole != nil {
		if r := *p.DefaultRole; r != authz.None && r != authz.Reader && r != authz.Author {
			return Space{}, errors.Join(ErrInvalid, errors.New("default_role: reader or author only"))
		}
		setDef, def = true, p.DefaultRole.String()
	}
	sp, err := scan(s.pool.QueryRow(ctx, `
UPDATE spaces SET
  name             = COALESCE($2, name),
  description      = COALESCE($3, description),
  require_approval = COALESCE($4, require_approval),
  default_role     = CASE WHEN $5 THEN NULLIF($6, '') ELSE default_role END
WHERE id = $1 AND type = 'team'
RETURNING id, slug, name, description, type, owner_user_id, require_approval, COALESCE(default_role, ''), status = 'archived'`,
		id, p.Name, p.Description, p.RequireApproval, setDef, def))
	if errors.Is(err, ErrNotFound) {
		return Space{}, s.notFoundOrNotTeam(ctx, id)
	}
	if err == nil {
		s.changed()
	}
	return sp, err
}

// ConvertPersonal turns a personal space into a team space (offboarding). Entries, their
// IDs and version history stay; members are added by the caller. The former owner gets a
// fresh personal space on the next login.
func (s *Store) ConvertPersonal(ctx context.Context, id uuid.UUID, p CreateParams) (Space, error) {
	if err := p.validate(); err != nil {
		return Space{}, err
	}
	sp, err := scan(s.pool.QueryRow(ctx, `
UPDATE spaces SET type = 'team', owner_user_id = NULL, slug = $2, name = $3, description = $4,
  require_approval = $5, default_role = NULLIF($6, '')
WHERE id = $1 AND type = 'personal'
RETURNING id, slug, name, description, type, owner_user_id, require_approval, COALESCE(default_role, ''), status = 'archived'`,
		id, p.Slug, strings.TrimSpace(p.Name), p.Description, p.RequireApproval, p.DefaultRole.String()))
	if isUnique(err) {
		return Space{}, ErrSlugTaken
	}
	if err == nil {
		s.changed()
	}
	return sp, err
}

// SetArchived archives or restores a team space.
func (s *Store) SetArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	status := "active"
	if archived {
		status = "archived"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE spaces SET status = $2 WHERE id = $1 AND type = 'team'`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.notFoundOrNotTeam(ctx, id)
	}
	s.changed()
	return nil
}

func (s *Store) notFoundOrNotTeam(ctx context.Context, id uuid.UUID) error {
	if _, err := s.GetByID(ctx, id); err != nil {
		return err
	}
	return ErrNotTeam
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// ListAllTeam returns every team space including archived ones (admin views).
func (s *Store) ListAllTeam(ctx context.Context) ([]Space, error) {
	rows, err := s.pool.Query(ctx, spaceSelect+`WHERE type = 'team' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		sp, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}
