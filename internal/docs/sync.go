package docs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// A tool can keep entries up to date from an outside source, for example the Markdown files of a Git
// repository. Every such entry has a stable key (sync_key, unique in its space) and belongs to the
// account of the tool (managed_by): the tool finds the entry by the key and replaces it, and nobody
// else changes or moves it. The functions here are the store; who may call them is decided by the
// tools that use them.

// MaxSyncKeyLen is the longest sync key, in characters.
const MaxSyncKeyLen = 500

// maxSyncList is the most entries SyncList returns; a longer list needs a longer prefix.
const maxSyncList = 20000

// ValidSyncKey checks a sync key: 1 to MaxSyncKeyLen characters, no control or invisible formatting
// characters, no white space at either end.
func ValidSyncKey(k string) error {
	if k == "" || len([]rune(k)) > MaxSyncKeyLen {
		return invalid("a sync key needs 1 to 500 characters")
	}
	if k != strings.TrimSpace(k) {
		return invalid("a sync key must not start or end with white space")
	}
	for _, r := range k {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return invalid("a sync key must not contain control or invisible characters")
		}
	}
	return nil
}

// SyncParams describes one entry to keep up to date.
type SyncParams struct {
	SpaceID uuid.UUID
	// Key identifies the entry in the space.
	Key     string
	Title   string
	Content string
	Tags    []string
	// Type is the knowledge type (empty: documentation).
	Type string
	// OwnerID names the responsible person; nil leaves the one the entry has.
	OwnerID *uuid.UUID
	// Origin is the reference to the original and the SHA-256 of the text taken over; both are required.
	Origin Origin
}

// What a Sync did.
const (
	SyncCreated   = "created"
	SyncUpdated   = "updated"
	SyncUnchanged = "unchanged"
	SyncRestored  = "restored" // the entry was archived and is back, with the new text if it changed
)

// SyncResult is the entry after Sync and what happened to it.
type SyncResult struct {
	Document Document
	Action   string
}

// Sync stores the entry under the key, or replaces the text of the entry that has the key. An entry whose
// text, tags, type, responsible person and origin are all as they are is left alone (no new version), and
// one that only differs in the origin or the responsible person gets no new version either. The same rules
// of space, role and approval apply as for any write, so an author in a space with approval writes
// drafts and cannot replace approved entries. An archived entry with the key comes back.
func (s *Store) Sync(ctx context.Context, a Actor, p SyncParams) (SyncResult, error) {
	if err := ValidSyncKey(p.Key); err != nil {
		return SyncResult{}, err
	}
	if strings.TrimSpace(p.Origin.SHA256) == "" {
		return SyncResult{}, invalid("a sync needs the sha256 of the text it takes over")
	}
	origins, err := NormalizeOrigins([]Origin{p.Origin})
	if err != nil {
		return SyncResult{}, err
	}
	sp, _, err := s.writableSpace(ctx, a, p.SpaceID)
	if err != nil {
		return SyncResult{}, err
	}
	typ := p.Type
	if typ == "" {
		typ = TypeDocumentation
	}

	var (
		id      uuid.UUID
		managed *uuid.UUID
		status  string
	)
	err = s.pool.QueryRow(ctx, `SELECT id, managed_by, status FROM documents WHERE space_id = $1 AND sync_key = $2`, sp.ID, p.Key).
		Scan(&id, &managed, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		res, err := s.Store(ctx, a, StoreParams{SpaceID: sp.ID, Title: p.Title, Content: p.Content, Tags: p.Tags,
			Force: true, // the key is the identity of the entry, not its similarity to others
			Type:  typ, OwnerID: p.OwnerID, Origins: origins, syncKey: p.Key, managedBy: &a.User.ID})
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Document: *res.Document, Action: SyncCreated}, nil
	}
	if err != nil {
		return SyncResult{}, err
	}
	if managed != nil && *managed != a.User.ID {
		return SyncResult{}, ErrManaged
	}

	restored := false
	if status == "archived" {
		if _, err := s.Unarchive(ctx, a, id); err != nil {
			return SyncResult{}, err
		}
		restored = true
	}
	cur, err := fetch(ctx, s.pool, a, id, false)
	if err != nil {
		return SyncResult{}, err
	}
	n, err := s.ing.Validate(p.Title, p.Content, p.Tags)
	if err != nil {
		return SyncResult{}, err
	}

	up := UpdateParams{ID: id}
	changed := false
	if n.Title != cur.Title {
		up.Title, changed = &n.Title, true
	}
	if n.Content != cur.Content {
		up.Content, changed = &n.Content, true
	}
	if !slices.Equal(n.Tags, cur.Tags) {
		up.Tags, changed = &n.Tags, true
	}
	if typ != cur.Type {
		up.Type, changed = &typ, true
	}
	if p.OwnerID != nil && (cur.OwnerID == nil || *cur.OwnerID != *p.OwnerID) {
		up.OwnerID, changed = p.OwnerID, true
	}
	if !slices.Equal(origins, cur.Origins) {
		up.Origins, changed = &origins, true
	}
	if !changed {
		action := SyncUnchanged
		if restored {
			action = SyncRestored
		}
		return SyncResult{Document: cur, Action: action}, nil
	}
	doc, err := s.Update(ctx, a, up)
	if err != nil {
		return SyncResult{}, err
	}
	action := SyncUpdated
	if restored {
		action = SyncRestored
	}
	return SyncResult{Document: doc, Action: action}, nil
}

// SyncEntry is one entry that a tool keeps up to date, as SyncList shows it.
type SyncEntry struct {
	Key     string
	ID      uuid.UUID
	Title   string
	Version int
	Status  string
	// SHA256 is the hash of the text the entry was last taken from (its origin), so that the tool sees
	// which sources changed without loading every entry.
	SHA256 string
}

// SyncList lists the live entries of the space that the actor keeps up to date and whose key starts with
// prefix (empty: all of them), ordered by key. Archived entries are not listed: a source that comes back is
// stored again by Sync, which restores the entry.
func (s *Store) SyncList(ctx context.Context, a Actor, spaceID uuid.UUID, prefix string) ([]SyncEntry, error) {
	if !a.Roles.Can(spaceID, authz.Reader) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
SELECT sync_key, id, title, version, status, COALESCE(origins -> 0 ->> 'sha256', '')
  FROM documents
 WHERE space_id = $1 AND managed_by = $2 AND starts_with(sync_key, $3) AND status <> 'archived'
 ORDER BY sync_key LIMIT $4`, spaceID, a.User.ID, prefix, maxSyncList+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncEntry
	for rows.Next() {
		var e SyncEntry
		if err := rows.Scan(&e.Key, &e.ID, &e.Title, &e.Version, &e.Status, &e.SHA256); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > maxSyncList {
		return nil, invalid("more than 20000 entries: use a longer key prefix")
	}
	return out, nil
}

// SyncArchive archives the live entry that the actor keeps up to date under the key (a source that is gone).
// It is ErrNotFound if there is none.
func (s *Store) SyncArchive(ctx context.Context, a Actor, spaceID uuid.UUID, key string) error {
	if !a.Roles.Can(spaceID, authz.Reader) {
		return ErrNotFound
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM documents
		WHERE space_id = $1 AND sync_key = $2 AND managed_by = $3 AND status <> 'archived'`, spaceID, key, a.User.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.Archive(ctx, a, id)
}
