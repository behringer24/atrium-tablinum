package docs

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/notify"
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
// In a space with mandatory approval a curator's own entries start as drafts and the curator may
// release them afterwards: the draft makes the release a deliberate step. In a space without
// mandatory approval a draft is an explicit request for a second look, so curators cannot release
// their own there. Admins always can, to untie the knot when nobody else is left to review. The
// audit log marks a release of one's own draft as self. Rejected entries go back to draft once
// the author revises them (Update).
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
	if own && role < authz.Admin && !sp.RequireApproval {
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
	// The author hears of the decision. In a personal space the draft was sent by somebody else, who
	// has no access to the copy and is told nothing.
	if sp.Type == "team" && !own {
		kind := notify.DraftReturned
		if p.Approve {
			kind = notify.DraftApproved
		}
		if err := notify.Emit(ctx, s.opt.Notify, tx, notify.Event{Kind: kind, SpaceID: &sp.ID, SubjectID: &p.ID, SubjectKind: "knowledge",
			ActorID: &a.User.ID, RecipientID: &locked.AuthorID}); err != nil {
			return Document{}, err
		}
	}
	doc, err := fetch(ctx, tx, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}
