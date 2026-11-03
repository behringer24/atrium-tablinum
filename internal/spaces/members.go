package spaces

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

const (
	PrincipalUser  = "user"
	PrincipalGroup = "group"
)

type Member struct {
	PrincipalType string
	PrincipalID   uuid.UUID
	Name          string // user e-mail or group name
	Role          authz.Role
}

// SetMember adds a user or group to a team space or changes its role. Because
// space_members.principal_id has no foreign key, existence is checked here.
func (s *Store) SetMember(ctx context.Context, space uuid.UUID, principalType string, principal uuid.UUID, role authz.Role, by uuid.UUID) error {
	if role == authz.None {
		return errors.Join(ErrInvalid, errors.New("role is missing"))
	}
	var table string
	switch principalType {
	case PrincipalUser:
		table = "users"
	case PrincipalGroup:
		table = "groups"
	default:
		return errors.Join(ErrInvalid, errors.New("invalid principal_type"))
	}
	if err := s.requireTeam(ctx, space); err != nil {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, principal).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO space_members (space_id, principal_type, principal_id, role, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (space_id, principal_type, principal_id) DO UPDATE SET role = EXCLUDED.role`,
		space, principalType, principal, role.String(), by)
	if err == nil {
		s.changed()
	}
	return err
}

func (s *Store) RemoveMember(ctx context.Context, space uuid.UUID, principalType string, principal uuid.UUID) error {
	if err := s.requireTeam(ctx, space); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM space_members WHERE space_id = $1 AND principal_type = $2 AND principal_id = $3`,
		space, principalType, principal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.changed()
	return nil
}

// Members lists the explicit memberships of a space (not default_role access).
func (s *Store) Members(ctx context.Context, space uuid.UUID) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
SELECT m.principal_type, m.principal_id, COALESCE(u.email::text, g.name, ''), m.role
FROM space_members m
LEFT JOIN users  u ON m.principal_type = 'user'  AND u.id = m.principal_id
LEFT JOIN groups g ON m.principal_type = 'group' AND g.id = m.principal_id
WHERE m.space_id = $1
ORDER BY m.principal_type DESC, 3`, space)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var (
			m    Member
			role string
		)
		if err := rows.Scan(&m.PrincipalType, &m.PrincipalID, &m.Name, &role); err != nil {
			return nil, err
		}
		m.Role, _ = authz.ParseRole(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) requireTeam(ctx context.Context, id uuid.UUID) error {
	sp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if sp.Type != "team" {
		return ErrNotTeam
	}
	return nil
}
