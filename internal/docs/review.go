package docs

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
)

const maxReviewNoteLen = 1000

type ReviewParams struct {
	ID      uuid.UUID
	Approve bool
	// Note is required when rejecting, the author reads it as review_note. Optional when
	// approving.
	Note string
}

// Review releases a draft or sends it back. Only curators of the entry's space may do this.
// A draft is a request for a second look, so curators cannot release their own; admins can,
// to untie the knot when nobody else is left to review (the audit log marks it as self).
// Rejected entries go back to draft once the author revises them (Update).
func (s *Store) Review(ctx context.Context, a Actor, p ReviewParams) (Document, error) {
	note := strings.TrimSpace(p.Note)
	if utf8.RuneCountInString(note) > maxReviewNoteLen {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("the note is too long"))
	}
	if !p.Approve && note == "" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("a note for the author is required when rejecting"))
	}
	cur, err := fetch(ctx, s.pool, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	sp, role, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return Document{}, err
	}
	if role < authz.Curator {
		return Document{}, ErrForbidden
	}
	if cur.Kind != "knowledge" || cur.Status != "draft" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("only drafts can be reviewed"))
	}
	own := cur.AuthorID == a.User.ID
	if own && role < authz.Admin {
		return Document{}, ErrOwnDraft
	}

	status, action := "rejected", "reject"
	if p.Approve {
		status, action = "approved", "approve"
	}
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
	if _, err := tx.Exec(ctx, `UPDATE documents SET status = $2, review_note = NULLIF($3, ''),
		version = $4, updated_at = now(), updated_by = $5 WHERE id = $1`,
		p.ID, status, note, version, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, p.ID, version, sp.ID, locked.Title, locked.Content, locked.Tags, status, a.User.ID); err != nil {
		return Document{}, err
	}
	details := map[string]any{"version": version}
	if own {
		details["self"] = true
	}
	if err := record(ctx, tx, a, action, sp, p.ID, details); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}
