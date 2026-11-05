package docs

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

// Change is an approved knowledge entry that became visible or changed since a point in time.
type Change struct {
	Document
	// New is true when the entry was approved for the first time since then, false when an entry
	// that readers could already see changed since then.
	New bool
}

// ChangedSince lists the approved knowledge entries of the given spaces that were approved for the
// first time or changed since `since`, newest first, without content. Only what readers can see now
// appears: drafts, returned and archived entries never do.
func (s *Store) ChangedSince(ctx context.Context, a Actor, spaceIDs []uuid.UUID, since time.Time, limit int) ([]Change, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	sc := a.Scope()
	rows, err := s.pool.Query(ctx, `SELECT `+docListColumns+`,
       COALESCE((SELECT min(v.changed_at) FROM document_versions v
                 WHERE v.document_id = d.id AND v.status = 'approved'), d.created_at) >= $5
`+docFrom+`
WHERE `+visibility.Clause("d", 1, 2, 3)+`
  AND d.status = 'approved' AND d.kind = 'knowledge' AND d.space_id = ANY($4::uuid[]) AND d.updated_at >= $5
ORDER BY d.updated_at DESC LIMIT $6`,
		nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), nonNil(spaceIDs), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(append(docTargets(&c.Document), &c.New)...); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DraftCounts counts the knowledge drafts waiting for review per space, for those of the given
// spaces where the actor is curator; other spaces are left out.
func (s *Store) DraftCounts(ctx context.Context, a Actor, spaceIDs []uuid.UUID) (map[uuid.UUID]int, error) {
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
		WHERE kind = 'knowledge' AND status = 'draft' AND space_id = ANY($1::uuid[]) GROUP BY space_id`, curated)
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
