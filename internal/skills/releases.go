package skills

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// Release is a skill version that was released.
type Release struct {
	Name        string
	SpaceID     uuid.UUID
	Version     int
	Description string
	PublishedAt time.Time
}

// ReleasedSince lists the versions released since `since` in the given spaces that the actor can
// read, newest first. Only versions that are still released and skills that are not archived appear.
func (s *Store) ReleasedSince(ctx context.Context, a docs.Actor, spaceIDs []uuid.UUID, since time.Time, limit int) ([]Release, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	vis, _ := scope(a)
	rows, err := s.pool.Query(ctx, `
SELECT s.name, s.space_id, v.version, v.description, v.published_at
FROM skill_versions v JOIN skills s ON s.id = v.skill_id
WHERE v.status = 'published' AND v.published_at >= $3 AND s.archived_at IS NULL
  AND s.space_id = ANY($1::uuid[]) AND s.space_id = ANY($2::uuid[])
ORDER BY v.published_at DESC LIMIT $4`, nonNil(vis), nonNil(spaceIDs), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.Name, &r.SpaceID, &r.Version, &r.Description, &r.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DraftCounts counts the skill versions waiting for review per space, for those of the given spaces
// where the actor is curator; other spaces are left out.
func (s *Store) DraftCounts(ctx context.Context, a docs.Actor, spaceIDs []uuid.UUID) (map[uuid.UUID]int, error) {
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
	rows, err := s.pool.Query(ctx, `SELECT s.space_id, count(*) FROM skill_versions v JOIN skills s ON s.id = v.skill_id
		WHERE v.status = 'draft' AND s.archived_at IS NULL AND s.space_id = ANY($1::uuid[]) GROUP BY s.space_id`, curated)
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

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
