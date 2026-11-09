package docs

import (
	"context"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// ExportSpace hands every knowledge entry of a space to fn, one at a time and in every state
// (approved, draft, returned, archived), with all its metadata and without loading the space
// into memory. Only an admin of the space may do this: the admins of a team space, the instance
// admins (who hold the admin role in every team space) and the owner of a personal space.
// Everyone else gets ErrNotFound, exactly as for a space they cannot see. Handoffs are not part
// of an export. An error from fn stops the export and is returned.
func (s *Store) ExportSpace(ctx context.Context, a Actor, spaceID uuid.UUID, fn func(Document) error) error {
	if !a.Roles.Can(spaceID, authz.Admin) {
		return ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT `+docColumns+` `+docFrom+`
WHERE d.space_id = $1 AND d.kind = 'knowledge'
ORDER BY d.title, d.id`, spaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return err
		}
		if err := fn(d); err != nil {
			return err
		}
	}
	return rows.Err()
}
