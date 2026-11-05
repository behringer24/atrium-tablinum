package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// Knowledge types, responsible people and review dates: docs/design/knowledge-metadata.md.

// parseType reads a knowledge type; empty stays empty.
func parseType(s string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t != "" && !docs.ValidType(t) {
		return "", userErr("type must be rule, documentation, recommendation or experience.")
	}
	return t, nil
}

// parseReviewDue reads a review date (YYYY-MM-DD).
func parseReviewDue(s string) (*time.Time, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return nil, userErr("review_due must be a date (YYYY-MM-DD), or empty to remove it.")
	}
	return &d, nil
}

// readerUser resolves the e-mail of a person who is to become responsible for entries in a space:
// they must have logged in, must not be locked and must be able to read the space.
func (s *Server) readerUser(ctx context.Context, email string, spaceID uuid.UUID) (uuid.UUID, error) {
	u, err := s.knownUser(ctx, email)
	if err != nil {
		return uuid.Nil, err
	}
	if !u.Active {
		return uuid.Nil, userErr(fmt.Sprintf("%s is locked and cannot be made responsible.", u.Email))
	}
	roles, err := s.Checker.EffectiveRoles(ctx, u)
	if err != nil {
		return uuid.Nil, err
	}
	if !roles.Can(spaceID, authz.Reader) {
		return uuid.Nil, userErr(fmt.Sprintf("%s cannot read this space and so cannot be responsible for an entry in it.", u.Email))
	}
	return u.ID, nil
}

// ---- reassign_owner ---------------------------------------------------------------------------

type reassignIn struct {
	From    string `json:"from" jsonschema:"E-mail of the person who is responsible now (also a locked, former person)"`
	To      string `json:"to" jsonschema:"E-mail of the person who takes over; must be active and able to read the space"`
	Space   string `json:"space,omitempty" jsonschema:"Optional: only this space (slug or name); empty = all spaces you administer"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"true only after explicit confirmation by the user; without it the call only reports how many entries are concerned"`
}

type reassignSpace struct {
	Space   string `json:"space"`
	Entries int    `json:"entries"`
}

type reassignOut struct {
	Message    string          `json:"message"`
	Reassigned []reassignSpace `json:"reassigned"`
	// Skipped lists spaces where `to` cannot read, with the number of entries left as they are.
	Skipped []reassignSpace `json:"skipped,omitempty"`
}

// reassignOwner moves the responsibility for entries from one person to another, for example when
// somebody leaves or changes jobs. Space admins act on the spaces they administer, instance admins
// on every team space.
func (s *Server) reassignOwner(ctx context.Context, c *call, in reassignIn) (reassignOut, error) {
	a := c.Actor
	admin, err := s.teamSpacesAtLeast(ctx, a, authz.Admin)
	if err != nil {
		return reassignOut{}, err
	}
	scope := admin
	if strings.TrimSpace(in.Space) != "" {
		sp, cands := matchSpace(admin, in.Space)
		switch {
		case sp != nil:
			scope = []spaces.Space{*sp}
		case len(cands) > 1:
			return reassignOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", in.Space, spaceNames(cands)))
		default:
			return reassignOut{}, userErr(fmt.Sprintf("Space %q not found among the spaces you administer.", in.Space))
		}
	}
	from, err := s.knownUser(ctx, in.From)
	if err != nil {
		return reassignOut{}, err
	}
	to, err := s.knownUser(ctx, in.To)
	if err != nil {
		return reassignOut{}, err
	}
	if !to.Active {
		return reassignOut{}, userErr(fmt.Sprintf("%s is locked and cannot take over.", to.Email))
	}
	if from.ID == to.ID {
		return reassignOut{}, userErr("from and to are the same person.")
	}
	owned, err := s.Docs.OwnedCounts(ctx, from.ID)
	if err != nil {
		return reassignOut{}, err
	}
	toRoles, err := s.Checker.EffectiveRoles(ctx, to)
	if err != nil {
		return reassignOut{}, err
	}
	var target []uuid.UUID
	names := map[uuid.UUID]string{}
	out := reassignOut{Reassigned: []reassignSpace{}}
	pending := 0
	for _, sp := range scope {
		names[sp.ID] = sp.Name
		n := owned[sp.ID]
		if n == 0 {
			continue
		}
		if !toRoles.Can(sp.ID, authz.Reader) {
			out.Skipped = append(out.Skipped, reassignSpace{Space: sp.Name, Entries: n})
			continue
		}
		target = append(target, sp.ID)
		pending += n
	}
	if !in.Confirm {
		return reassignOut{}, userErr(fmt.Sprintf("%s is responsible for %d entries in the spaces concerned that %s can read%s. "+
			"Tell the user, ask for confirmation, then call again with confirm=true.", from.Email, pending, to.Email, skippedNote(out.Skipped)))
	}
	moved, err := s.Docs.ReassignOwner(ctx, a, from.ID, to.ID, target)
	if err != nil {
		return reassignOut{}, err
	}
	total := 0
	for _, r := range moved {
		total += r.Entries
		out.Reassigned = append(out.Reassigned, reassignSpace{Space: names[r.SpaceID], Entries: r.Entries})
		sp := spaces.Space{ID: r.SpaceID}
		s.record(ctx, a, "owner_reassign", &sp, map[string]any{"from": from.Email, "to": to.Email, "entries": r.Entries})
	}
	sort.Slice(out.Reassigned, func(i, j int) bool { return out.Reassigned[i].Space < out.Reassigned[j].Space })
	out.Message = fmt.Sprintf("%s is now responsible for %d entries that %s was responsible for.%s", to.Email, total, from.Email, skippedNote(out.Skipped))
	return out, nil
}

func skippedNote(skipped []reassignSpace) string {
	if len(skipped) == 0 {
		return ""
	}
	parts := make([]string, 0, len(skipped))
	for _, sk := range skipped {
		parts = append(parts, fmt.Sprintf("%s (%d)", sk.Space, sk.Entries))
	}
	return " Left as they are, because the new person cannot read these spaces: " + strings.Join(parts, ", ") + "."
}
