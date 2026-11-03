// Package visibility holds the one SQL predicate that decides which documents a caller
// may see. Every query on documents/chunks must include it (concept section 3.3).
package visibility

import "fmt"

// Clause returns a predicate on the documents table aliased as alias. The numbers are
// the positional parameters that carry, as uuid[] / uuid:
//
//	visible  – spaces where the caller holds at least reader
//	actor    – the caller's user id
//	curator  – spaces where the caller holds at least curator (they also see others' drafts)
//
// Archived documents never match; handoffs are covered because only the owner has a role
// in a personal space. Expired documents never match.
func Clause(alias string, visible, actor, curator int) string {
	return fmt.Sprintf(`(%[1]s.space_id = ANY($%[2]d::uuid[])
  AND (%[1]s.expires_at IS NULL OR %[1]s.expires_at > now())
  AND (%[1]s.status = 'approved'
       OR (%[1]s.status IN ('draft', 'rejected')
           AND (%[1]s.author_id = $%[3]d::uuid OR %[1]s.space_id = ANY($%[4]d::uuid[])))))`,
		alias, visible, actor, curator)
}

// ArchiveClause is the counterpart for archived documents: only those who may restore an
// entry see it, that is its author and the curators of its space. Parameters as in Clause.
// It is never combined with Clause except for a direct load by id.
func ArchiveClause(alias string, visible, actor, curator int) string {
	return fmt.Sprintf(`(%[1]s.space_id = ANY($%[2]d::uuid[])
  AND (%[1]s.expires_at IS NULL OR %[1]s.expires_at > now())
  AND %[1]s.status = 'archived'
  AND (%[1]s.author_id = $%[3]d::uuid OR %[1]s.space_id = ANY($%[4]d::uuid[])))`,
		alias, visible, actor, curator)
}
