package users

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrGroupExists   = errors.New("group already exists")
	ErrGroupReadOnly = errors.New("idp groups are read-only")
)

type Group struct {
	ID          uuid.UUID
	Name        string
	Source      string // local | idp
	IDP         string
	ExternalID  string
	MemberCount int
}

const groupSelect = `
SELECT g.id, g.name, g.source, COALESCE(g.idp, ''), COALESCE(g.external_id, ''),
       (SELECT count(*) FROM group_members m WHERE m.group_id = g.id)
FROM groups g `

func (s *Store) CreateGroup(ctx context.Context, name string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, errors.New("group name missing")
	}
	var g Group
	err := s.pool.QueryRow(ctx, `INSERT INTO groups (name, source) VALUES ($1, 'local') RETURNING id, name, source`,
		name).Scan(&g.ID, &g.Name, &g.Source)
	if isUniqueViolation(err) {
		return Group{}, ErrGroupExists
	}
	return g, wrap("create group", err)
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx, groupSelect+`ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Source, &g.IDP, &g.ExternalID, &g.MemberCount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GroupByName resolves a group case-insensitively.
func (s *Store) GroupByName(ctx context.Context, name string) (Group, error) {
	var g Group
	err := s.pool.QueryRow(ctx, groupSelect+`WHERE lower(g.name) = lower($1)`, strings.TrimSpace(name)).
		Scan(&g.ID, &g.Name, &g.Source, &g.IDP, &g.ExternalID, &g.MemberCount)
	if err != nil {
		return Group{}, mapNoRows(err)
	}
	return g, nil
}

func (s *Store) localGroup(ctx context.Context, id uuid.UUID) error {
	var source string
	if err := s.pool.QueryRow(ctx, `SELECT source FROM groups WHERE id = $1`, id).Scan(&source); err != nil {
		return mapNoRows(err)
	}
	if source != "local" {
		return ErrGroupReadOnly
	}
	return nil
}

func (s *Store) RenameGroup(ctx context.Context, id uuid.UUID, name string) error {
	if err := s.localGroup(ctx, id); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE groups SET name = $2 WHERE id = $1`, id, strings.TrimSpace(name))
	if isUniqueViolation(err) {
		return ErrGroupExists
	}
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	if err := s.localGroup(ctx, id); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *Store) AddGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.localGroup(ctx, groupID); err != nil {
		return err
	}
	if _, err := s.Get(ctx, userID); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, groupID, userID); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.localGroup(ctx, groupID); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID); err != nil {
		return err
	}
	s.changed()
	return nil
}

// GroupMembers lists member e-mails of a group.
func (s *Store) GroupMembers(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT u.email FROM group_members m JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1 ORDER BY u.email`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GroupNamesOf lists the names of all groups the user belongs to.
func (s *Store) GroupNamesOf(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.name FROM group_members m JOIN groups g ON g.id = m.group_id
		WHERE m.user_id = $1 ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
