package docs

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// updateMeta sets the responsible person and the review date of an entry. Neither changes what the
// entry says, so no version is written, the entry is not re-indexed and it does not count as changed
// for whats_new; the audit log records the change.
func (s *Store) updateMeta(ctx context.Context, a Actor, sp spaces.Space, cur Document, owner *uuid.UUID, reviewDue *time.Time) (Document, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(ctx)
	locked, err := fetch(ctx, tx, a, cur.ID, true)
	if err != nil {
		return Document{}, err
	}
	if locked.Version != cur.Version {
		return Document{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET owner_id = $2, review_due_at = $3 WHERE id = $1`, cur.ID, owner, reviewDue); err != nil {
		return Document{}, err
	}
	details := map[string]any{"owner_changed": !sameID(owner, cur.OwnerID), "review_due_changed": !sameTime(reviewDue, cur.ReviewDue)}
	if err := record(ctx, tx, a, "update_meta", sp, cur.ID, details); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, cur.ID, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}

func sameID(a, b *uuid.UUID) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func sameTime(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }

// Reassigned is the result of ReassignOwner for one space.
type Reassigned struct {
	SpaceID uuid.UUID
	Entries int
}

// ReassignOwner makes `to` responsible for every knowledge entry `from` is responsible for in the
// given team spaces, where the actor must be admin. The caller passes only spaces that `to` can
// read, so that nobody becomes responsible for what they cannot see, and records the audit entries.
// It writes no version: who answers for an entry is no change of what it says.
func (s *Store) ReassignOwner(ctx context.Context, a Actor, from, to uuid.UUID, spaceIDs []uuid.UUID) ([]Reassigned, error) {
	var ids []uuid.UUID
	for _, id := range spaceIDs {
		if a.Roles.Can(id, authz.Admin) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `WITH moved AS (
  UPDATE documents SET owner_id = $2 WHERE owner_id = $1 AND kind = 'knowledge' AND space_id = ANY($3::uuid[])
  RETURNING space_id)
SELECT space_id, count(*) FROM moved GROUP BY space_id`, from, to, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reassigned
	for rows.Next() {
		var r Reassigned
		if err := rows.Scan(&r.SpaceID, &r.Entries); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OwnedCounts counts, per space, the active knowledge entries (not archived) a person is
// responsible for. It is for administrators deciding about a reassignment and reveals no titles.
func (s *Store) OwnedCounts(ctx context.Context, owner uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT space_id, count(*) FROM documents
		WHERE owner_id = $1 AND kind = 'knowledge' AND status <> 'archived' GROUP BY space_id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ReviewsDueCounts counts the approved entries whose review date has passed, per space, for those of
// the given spaces where the actor is curator.
func (s *Store) ReviewsDueCounts(ctx context.Context, a Actor, spaceIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	var curated []uuid.UUID
	for _, id := range spaceIDs {
		if a.Roles.Can(id, authz.Curator) {
			curated = append(curated, id)
		}
	}
	out := map[uuid.UUID]int{}
	if len(curated) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT space_id, count(*) FROM documents
		WHERE kind = 'knowledge' AND status = 'approved' AND review_due_at < now() AND space_id = ANY($1::uuid[])
		GROUP BY space_id`, curated)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
