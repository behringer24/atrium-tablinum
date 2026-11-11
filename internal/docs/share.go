package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/share"
)

// ShareParams names the entry to send and the recipient's e-mail address.
type ShareParams struct {
	ID    uuid.UUID
	Email string
}

// Share sends a copy of an approved entry to the personal space of another user, where it waits
// as a draft for the recipient, who is the only one who can approve or return it (Review).
//
// The sender needs the right to change the entry (canEdit: its author with the author role, or a
// curator of its space; the owner for an entry in their own personal space), so that sending is
// no way to hand content to people who cannot read the space. A binding rule is sent as plain
// documentation, because rules exist only in team spaces. The copy carries the sender as author.
// Everything that goes wrong on the recipient's side is the same ErrUndeliverable, and the
// recipient's existing entries are not checked for duplicates, or the answer would reveal them.
func (s *Store) Share(ctx context.Context, a Actor, p ShareParams) error {
	if !s.opt.Share.Enabled {
		return share.ErrDisabled
	}
	email := strings.TrimSpace(p.Email)
	if email == "" {
		return errors.Join(ingest.ErrInvalid, errors.New("the recipient's e-mail address is required"))
	}
	cur, err := fetch(ctx, s.pool, a, p.ID, false)
	if err != nil {
		return err
	}
	if cur.Kind != "knowledge" || cur.Status != "approved" {
		return errors.Join(ingest.ErrInvalid, errors.New("only approved knowledge entries can be sent"))
	}
	if err := canEdit(a, cur); err != nil {
		return err
	}
	if _, _, err := s.writableSpace(ctx, a, cur.SpaceID); err != nil {
		return err
	}
	n, err := s.ing.Validate(cur.Title, cur.Content, cur.Tags)
	if err != nil {
		return err
	}
	to, err := share.Resolve(ctx, s.pool, a.User.ID, email)
	if err != nil {
		return err
	}
	dst, err := s.spaces.GetByID(ctx, to.SpaceID)
	if err != nil {
		return share.ErrUndeliverable
	}
	if err := s.checkQuota(ctx, dst, 1, int64(len(n.Content))); err != nil {
		if errors.Is(err, ErrQuota) {
			return share.ErrUndeliverable
		}
		return err
	}
	prep, err := s.ing.Prepare(ctx, n.Title, n.Content)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := share.Reserve(ctx, tx, s.opt.Share, to, a.User.ID); err != nil {
		return err
	}
	typ := cur.Type
	if typ == TypeRule {
		typ = TypeDocumentation
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO documents (space_id, kind, title, content, tags, status, author_id, source, updated_by, knowledge_type)
VALUES ($1, 'knowledge', $2, $3, $4, 'draft', $5, $6, $5, $7) RETURNING id`,
		to.SpaceID, n.Title, n.Content, n.Tags, a.User.ID, source(a), typ).Scan(&id)
	if err != nil {
		return err
	}
	if err := writeVersion(ctx, tx, id, 1, to.SpaceID, n.Title, n.Content, n.Tags, "draft", a.User.ID); err != nil {
		return err
	}
	if err := s.ing.ReplaceChunks(ctx, tx, id, prep); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(n.Title + "\n" + n.Content))
	if err := share.Audit(ctx, tx, source(a), a.User.ID, "knowledge", to, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
