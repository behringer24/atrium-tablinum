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
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.space_id, d.kind, d.title, '', d.tags, d.status, COALESCE(d.review_note, ''),
       d.author_id, au.email::text, d.source, d.version, d.expires_at, d.created_at, d.updated_at,
       COALESCE((SELECT min(v.changed_at) FROM document_versions v
                 WHERE v.document_id = d.id AND v.status = 'approved'), d.created_at) >= $5
FROM documents d JOIN users au ON au.id = d.author_id
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
		d := &c.Document
		if err := rows.Scan(&d.ID, &d.SpaceID, &d.Kind, &d.Title, &d.Content, &d.Tags, &d.Status, &d.ReviewNote,
			&d.AuthorID, &d.AuthorEmail, &d.Source, &d.Version, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt, &c.New); err != nil {
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
