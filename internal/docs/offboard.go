package docs

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// Offboarding works on the personal space of a disabled account, which nobody but its
// owner may see. These methods therefore skip the per-entry permission checks; the MCP
// layer calls them only for instance admins and disabled accounts.

// DeleteHandoffs removes every handoff of a personal space. Handoffs are short-lived
// working notes and are never handed on.
func (s *Store) DeleteHandoffs(ctx context.Context, space uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM documents WHERE space_id = $1 AND kind = 'handoff'`, space)
	return tag.RowsAffected(), err
}

// MergePersonal moves all entries of one personal space into the personal space of the
// receiver, who then owns them like their own. Handoffs are deleted. The receiver's quota
// applies; on a violation nothing is moved.
func (s *Store) MergePersonal(ctx context.Context, from uuid.UUID, to users.User, by uuid.UUID) (int, error) {
	if to.PersonalSpaceID == nil {
		return 0, errNoPersonalSpace
	}
	if *to.PersonalSpaceID == from {
		return 0, errors.Join(ingest.ErrInvalid, errors.New("source and target are the same space"))
	}
	var (
		docs  int
		bytes int64
	)
	if err := s.pool.QueryRow(ctx, `
SELECT count(*), COALESCE(sum(octet_length(content)), 0)
FROM documents WHERE space_id = $1 AND kind = 'knowledge' AND status <> 'archived'`, from).Scan(&docs, &bytes); err != nil {
		return 0, err
	}
	// The limits of the receiver's personal space apply.
	recv, err := s.spaces.GetByID(ctx, *to.PersonalSpaceID)
	if err != nil {
		return 0, err
	}
	if err := s.checkQuota(ctx, recv, docs, bytes); err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE space_id = $1 AND kind = 'handoff'`, from); err != nil {
		return 0, err
	}
	// Chunks carry no space; search and visibility follow documents.space_id.
	tag, err := tx.Exec(ctx, `
WITH moved AS (
  UPDATE documents SET space_id = $2, version = version + 1, review_note = NULL, updated_at = now(), updated_by = $3
  WHERE space_id = $1 AND kind = 'knowledge'
  RETURNING id, version, title, content, tags, status, knowledge_type)
INSERT INTO document_versions (document_id, version, space_id, title, content, tags, status, changed_by, knowledge_type)
SELECT id, version, $2, title, content, tags, status, $3, knowledge_type FROM moved`, from, *to.PersonalSpaceID, by)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// PurgeOffboarded deletes the personal spaces of accounts disabled for more than
// retentionDays, including all entries. Spaces converted to team spaces are no longer
// personal and stay. Every purge is audited with the account and a count, never content.
func (s *Store) PurgeOffboarded(ctx context.Context, retentionDays int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
WITH doomed AS (
  SELECT sp.id, u.email::text AS email,
         (SELECT count(*) FROM documents d WHERE d.space_id = sp.id AND d.kind = 'knowledge') AS docs
  FROM spaces sp JOIN users u ON u.id = sp.owner_user_id
  WHERE sp.type = 'personal' AND u.status = 'disabled' AND u.disabled_at < now() - make_interval(days => $1))
DELETE FROM spaces sp USING doomed WHERE sp.id = doomed.id
RETURNING sp.id, doomed.email, doomed.docs`, retentionDays)
	if err != nil {
		return 0, err
	}
	type purged struct {
		space uuid.UUID
		email string
		docs  int
	}
	var done []purged
	for rows.Next() {
		var p purged
		if err := rows.Scan(&p.space, &p.email, &p.docs); err != nil {
			rows.Close()
			return 0, err
		}
		done = append(done, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range done {
		if err := audit.Insert(ctx, tx, audit.Event{Client: "system", Action: "purge_personal", SpaceID: &p.space,
			Details: map[string]any{"user": p.email, "documents": p.docs}}); err != nil {
			return 0, err
		}
	}
	return len(done), tx.Commit(ctx)
}
