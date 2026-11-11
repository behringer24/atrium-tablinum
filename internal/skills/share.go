package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/share"
)

// Share sends a copy of the newest released version of a skill to the personal space of another
// user. Like Move it carries nothing of the release over: the copy has the same files (same
// digest) as a draft, the sender is its author, and the recipient, who owns the space, has to
// read, approve (review_skill, with a risk class) and release (publish_skill) it before their
// assistant offers it. A skill is an instruction, so a skill that arrives from somebody else is
// the most careful case of all.
//
// The sender needs curator rights in the source space (the owner in their own personal space),
// the same as for moving a skill, so that sending is no way around the review of a team space.
// Everything that goes wrong on the recipient's side, a name that is already taken there
// included, is the same share.ErrUndeliverable.
func (s *Store) Share(ctx context.Context, a docs.Actor, skillID uuid.UUID, email string) error {
	if !s.opt.Share.Enabled {
		return share.ErrDisabled
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return invalid("the recipient's e-mail address is required")
	}
	to, err := share.Resolve(ctx, s.pool, a.User.ID, email)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var name string
	var srcID uuid.UUID
	var tags []string
	var archived bool
	err = tx.QueryRow(ctx, `SELECT name, space_id, tags, archived_at IS NOT NULL FROM skills WHERE id = $1`, skillID).
		Scan(&name, &srcID, &tags, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, _, err := s.space(ctx, a, srcID, authz.Curator); err != nil {
		return err
	}
	if archived {
		return ErrArchived
	}
	var srcVersion uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM skill_versions WHERE skill_id = $1 AND status = 'published'
		ORDER BY version DESC LIMIT 1`, skillID).Scan(&srcVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: only a skill with a released version can be sent", ErrState)
	} else if err != nil {
		return err
	}
	v, err := loadVersion(ctx, tx, srcVersion)
	if err != nil {
		return err
	}
	if err := verifyStored(ctx, tx, v); err != nil {
		return err
	}
	dst, err := s.sp.GetByID(ctx, to.SpaceID)
	if err != nil {
		return share.ErrUndeliverable
	}

	if err := share.Reserve(ctx, tx, s.opt.Share, to, a.User.ID); err != nil {
		return err
	}
	var newSkill uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO skills (space_id, name, created_by, tags) VALUES ($1, $2, $3, $4)
		ON CONFLICT (space_id, name) DO NOTHING RETURNING id`, to.SpaceID, name, a.User.ID, tags).Scan(&newSkill)
	if errors.Is(err, pgx.ErrNoRows) {
		return share.ErrUndeliverable
	} else if err != nil {
		return err
	}
	if err := s.checkQuota(ctx, tx, dst, newSkill, a.User.ID, true, prepared{resources: v.Resources}); err != nil {
		if errors.Is(err, ErrQuota) {
			return share.ErrUndeliverable
		}
		return err
	}
	var newVersion uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO skill_versions (skill_id, version, description, requested_tools, digest, created_by)
		SELECT $1, 1, description, requested_tools, digest, $3 FROM skill_versions WHERE id = $2 RETURNING id`,
		newSkill, srcVersion, a.User.ID).Scan(&newVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO skill_resources (version_id, path, content, size, sha256)
		SELECT $1, path, content, size, sha256 FROM skill_resources WHERE version_id = $2`, newVersion, srcVersion); err != nil {
		return err
	}
	if err := share.Audit(ctx, tx, a.Client, a.User.ID, "skill", to, v.Digest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
