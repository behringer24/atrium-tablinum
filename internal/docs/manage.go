package docs

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
)

type MoveParams struct {
	ID      uuid.UUID
	SpaceID uuid.UUID // Zielbereich
}

// Move relocates an approved knowledge entry into another space. The actor needs the
// right to edit the entry in its current space and write access (author or higher) in the
// target space. Approval rules of the target apply: an entry moved into an approval space
// by a non-curator becomes a draft there.
func (s *Store) Move(ctx context.Context, a Actor, p MoveParams) (Document, error) {
	cur, err := fetch(ctx, s.pool, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	if cur.Kind != "knowledge" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("übergaben können nicht verschoben werden"))
	}
	if err := canEdit(a, cur); err != nil {
		return Document{}, err
	}
	src, srcRole, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return Document{}, err
	}
	if cur.Status != "approved" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("nur freigegebene einträge können verschoben werden"))
	}
	// Authors may not take approved entries out of an approval space on their own.
	if src.Type == "team" && src.RequireApproval && srcRole < authz.Curator {
		return Document{}, ErrNeedsApproval
	}
	dst, dstRole, err := s.writableSpace(ctx, a, p.SpaceID)
	if err != nil {
		return Document{}, err
	}
	if dst.ID == src.ID {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("der eintrag liegt bereits in diesem bereich"))
	}
	if dst.Type == "personal" {
		if err := s.checkQuota(ctx, a, dst.ID, 1, int64(len(cur.Content))); err != nil {
			return Document{}, err
		}
	}
	status := statusFor(dst, dstRole, false)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(ctx)
	locked, err := fetch(ctx, tx, a, p.ID, true)
	if err != nil {
		return Document{}, err
	}
	if locked.Version != cur.Version {
		return Document{}, ErrConflict
	}
	version := locked.Version + 1
	// Chunks carry no space; search and visibility follow documents.space_id.
	if _, err := tx.Exec(ctx, `UPDATE documents SET space_id = $2, status = $3, review_note = NULL,
		version = $4, updated_at = now(), updated_by = $5 WHERE id = $1`,
		p.ID, dst.ID, status, version, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, p.ID, version, dst.ID, locked.Title, locked.Content, locked.Tags, status, a.User.ID); err != nil {
		return Document{}, err
	}
	from := src.Slug
	if src.Type == "personal" {
		from = "personal"
	}
	if err := record(ctx, tx, a, "move", dst, p.ID, map[string]any{"from": from, "status": status}); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}

// Delete removes an entry for good, including chunks and version history. Only admins of
// the entry's space may do this (instance admins count as admins of every team space; the
// owner is admin of their personal space). Everyone else can archive instead.
func (s *Store) Delete(ctx context.Context, a Actor, id uuid.UUID) error {
	cur, err := fetch(ctx, s.pool, a, id, false)
	if err != nil {
		return err
	}
	if a.Roles.Role(cur.SpaceID) < authz.Admin {
		return ErrForbidden
	}
	sp, err := s.spaces.GetByID(ctx, cur.SpaceID)
	if err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	locked, err := fetch(ctx, tx, a, id, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id); err != nil {
		return err
	}
	// The title stays in the log of team spaces (the entry itself is gone); personal
	// spaces keep no details.
	if err := record(ctx, tx, a, "delete", sp, id, map[string]any{"title": locked.Title}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
