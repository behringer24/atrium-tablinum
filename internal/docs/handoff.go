package docs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

const maxHandoffTTLDays = 90

var errNoPersonalSpace = errors.Join(ingest.ErrInvalid, errors.New("personal spaces are disabled on this instance"))

// SaveHandoff stores a hand-over note in the caller's personal space. Handoffs are not
// chunked or embedded: they are excluded from search and loaded by their own tools.
func (s *Store) SaveHandoff(ctx context.Context, a Actor, title, content string, ttlDays int) (Document, error) {
	if a.User.PersonalSpaceID == nil {
		return Document{}, errNoPersonalSpace
	}
	space := *a.User.PersonalSpaceID
	sp, _, err := s.writableSpace(ctx, a, space)
	if err != nil {
		return Document{}, err
	}
	n, err := s.ing.Validate(title, content, nil)
	if err != nil {
		return Document{}, err
	}
	hsp, err := s.spaces.GetByID(ctx, space)
	if err != nil {
		return Document{}, err
	}
	if err := s.checkQuota(ctx, hsp, 0, int64(len(n.Content))); err != nil {
		return Document{}, err
	}
	if ttlDays < 1 {
		ttlDays = s.opt.HandoffTTLDays
	}
	ttlDays = min(ttlDays, maxHandoffTTLDays)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO documents (space_id, kind, title, content, tags, status, author_id, source, updated_by, expires_at)
VALUES ($1, 'handoff', $2, $3, '{}', 'approved', $4, $5, $4, $6) RETURNING id`,
		space, n.Title, n.Content, a.User.ID, source(a), handoffExpiry(ttlDays)).Scan(&id)
	if err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, id, 1, space, n.Title, n.Content, []string{}, "approved", a.User.ID); err != nil {
		return Document{}, err
	}
	if err := record(ctx, tx, a, "handoff_save", sp, id, nil); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, id, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}

// ListHandoffs returns the caller's unexpired handoffs, newest first, without content.
func (s *Store) ListHandoffs(ctx context.Context, a Actor, limit int) ([]Document, error) {
	if a.User.PersonalSpaceID == nil || !a.Roles.Can(*a.User.PersonalSpaceID, authz.Reader) {
		return nil, nil
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	sc := a.Scope()
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.space_id, d.kind, d.title, '', d.tags, d.status, '',
       d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at
FROM documents d JOIN users au ON au.id = d.author_id
WHERE `+visibility.Clause("d", 1, 2, 3)+` AND d.kind = 'handoff' AND d.space_id = $4
ORDER BY d.created_at DESC LIMIT $5`,
		nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), *a.User.PersonalSpaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}

// LoadHandoff returns the handoff with the given id, or the newest one when id is nil.
func (s *Store) LoadHandoff(ctx context.Context, a Actor, id *uuid.UUID) (Document, error) {
	if id != nil {
		d, err := s.Get(ctx, a, *id)
		if err != nil {
			return Document{}, err
		}
		if d.Kind != "handoff" {
			return Document{}, ErrNotFound
		}
		return d, nil
	}
	list, err := s.ListHandoffs(ctx, a, 1)
	if err != nil {
		return Document{}, err
	}
	if len(list) == 0 {
		return Document{}, ErrNotFound
	}
	return s.Get(ctx, a, list[0].ID)
}

// ExpireHandoffs deletes expired handoffs of all users (background job).
func (s *Store) ExpireHandoffs(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM documents WHERE kind = 'handoff' AND expires_at <= now()`)
	return tag.RowsAffected(), err
}

// PersonalStat is the metadata admins may see about a personal space (never content).
type PersonalStat struct {
	Documents  int
	Bytes      int64
	LastChange *time.Time
}

// PersonalStats returns count, size and last change of every user's personal space.
func (s *Store) PersonalStats(ctx context.Context) (map[uuid.UUID]PersonalStat, error) {
	rows, err := s.pool.Query(ctx, `
SELECT sp.owner_user_id,
       count(d.id) FILTER (WHERE d.kind = 'knowledge' AND d.status <> 'archived'),
       COALESCE(sum(octet_length(d.content)) FILTER (WHERE d.status <> 'archived'), 0),
       max(d.updated_at)
FROM spaces sp LEFT JOIN documents d ON d.space_id = sp.id
WHERE sp.type = 'personal' GROUP BY sp.owner_user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]PersonalStat{}
	for rows.Next() {
		var (
			id uuid.UUID
			st PersonalStat
		)
		if err := rows.Scan(&id, &st.Documents, &st.Bytes, &st.LastChange); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
