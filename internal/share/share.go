// Package share holds what knowledge entries and skills have in common when one user sends a
// copy to another: who the recipient is, how many unanswered items a personal space accepts, and
// the audit event. The copy itself is written by package docs and package skills, each in its own
// transaction, so the two never share a table or a read path.
//
// A sent item lands in the recipient's personal space as a draft. The recipient is the owner of
// that space and so its only reviewer: they approve (and for a skill release) or reject it with
// the ordinary review tools. The sender has no role in the recipient's space, so they can neither
// read the copy nor change it afterwards.
package share

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/audit"
)

var (
	// ErrDisabled: sending to other users is switched off for the instance (sharing.enabled).
	ErrDisabled = errors.New("sending to other users is not enabled on this server")
	// ErrSelf: the recipient is the sender.
	ErrSelf = errors.New("you cannot send something to yourself: it is already in your own space")
	// ErrUndeliverable is the one answer for everything on the recipient's side: unknown or
	// disabled account, no personal space, a name already taken there, a full space or too many
	// unanswered items. The sender must not learn which, or the tool would reveal who exists and
	// what they hold.
	ErrUndeliverable = errors.New("the item could not be delivered: the recipient is unknown, cannot receive it right now or has too many unanswered items")
)

const (
	DefaultMaxPerRecipient = 20
	DefaultMaxPerSender    = 3
)

// Options configures sending. The zero value is "off".
type Options struct {
	Enabled bool
	// MaxPerRecipient caps the items (entries and skill versions, sent by anybody) that wait in
	// one personal space, drafts and returned ones together; MaxPerSender caps those of one sender
	// in one personal space. They keep a sender from filling another user's review list or
	// quota. 0 = the default.
	MaxPerRecipient int
	MaxPerSender    int
}

// WithDefaults replaces unset limits by the defaults.
func (o Options) WithDefaults() Options {
	if o.MaxPerRecipient <= 0 {
		o.MaxPerRecipient = DefaultMaxPerRecipient
	}
	if o.MaxPerSender <= 0 {
		o.MaxPerSender = DefaultMaxPerSender
	}
	return o
}

// Recipient is the account an item is sent to and its personal space.
type Recipient struct {
	UserID  uuid.UUID
	SpaceID uuid.UUID
}

// Resolve finds the active account with the e-mail address and its active personal space. Every
// miss is ErrUndeliverable; the sender themselves is ErrSelf.
func Resolve(ctx context.Context, pool *pgxpool.Pool, sender uuid.UUID, email string) (Recipient, error) {
	var r Recipient
	err := pool.QueryRow(ctx, `SELECT u.id, sp.id FROM users u
		JOIN spaces sp ON sp.owner_user_id = u.id AND sp.type = 'personal' AND sp.status = 'active'
		WHERE u.email = $1 AND u.status = 'active'`, email).Scan(&r.UserID, &r.SpaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, ErrUndeliverable
	}
	if err != nil {
		return Recipient{}, err
	}
	if r.UserID == sender {
		return Recipient{}, ErrSelf
	}
	return r, nil
}

// Reserve takes a lock on the recipient's space for the rest of the transaction, so that two
// senders cannot both pass the limits when only one place is left, and checks the limits.
func Reserve(ctx context.Context, tx pgx.Tx, o Options, to Recipient, sender uuid.UUID) error {
	o = o.WithDefaults()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, to.SpaceID.String()); err != nil {
		return err
	}
	var all, own int
	// Items the owner wrote themselves never count: only what others sent.
	err := tx.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM documents WHERE space_id = $1 AND kind = 'knowledge' AND status IN ('draft', 'rejected') AND author_id <> $2)
+ (SELECT count(*) FROM skill_versions v JOIN skills s ON s.id = v.skill_id
    WHERE s.space_id = $1 AND s.archived_at IS NULL AND v.status IN ('draft', 'rejected') AND v.created_by IS NOT NULL AND v.created_by <> $2),
  (SELECT count(*) FROM documents WHERE space_id = $1 AND kind = 'knowledge' AND status IN ('draft', 'rejected') AND author_id = $3)
+ (SELECT count(*) FROM skill_versions v JOIN skills s ON s.id = v.skill_id
    WHERE s.space_id = $1 AND s.archived_at IS NULL AND v.status IN ('draft', 'rejected') AND v.created_by = $3)`,
		to.SpaceID, to.UserID, sender).Scan(&all, &own)
	if err != nil {
		return err
	}
	if all >= o.MaxPerRecipient || own >= o.MaxPerSender {
		return ErrUndeliverable
	}
	return nil
}

// Audit records the sending. It is an event of the sender, outside any space, and names the
// recipient by id and what was sent by kind and digest, never by title or name.
func Audit(ctx context.Context, tx pgx.Tx, client string, sender uuid.UUID, kind string, to Recipient, digest string) error {
	return audit.Insert(ctx, tx, audit.Event{
		UserID: &sender, Client: client, Action: "share_send",
		Details: map[string]any{"kind": kind, "recipient": to.UserID.String(), "digest": digest},
	})
}
