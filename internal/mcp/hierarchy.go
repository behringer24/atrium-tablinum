package mcpserver

import (
	"context"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// A team space may sit below another one and take over its memberships (internal/authz). The tools that
// list who holds a role in a space use the helpers here, so that what they show is what the role
// computation does.

// inheritedFrom lists the spaces whose memberships a space takes over, nearest first: walking up while
// the inheritance is on. A parent that is not in the map ends the walk, and so does a loop in the data.
func inheritedFrom(id uuid.UUID, links map[uuid.UUID]spaceLinkRow) []uuid.UUID {
	var out []uuid.UUID
	cur := id
	for range len(links) + 1 {
		l := links[cur]
		if l.parent == nil || !l.inherit {
			break
		}
		cur = *l.parent
		if _, ok := links[cur]; !ok {
			break
		}
		out = append(out, cur)
	}
	return out
}

// principalKey identifies the user or group of a membership.
func principalKey(m spaces.Member) string { return m.PrincipalType + ":" + m.PrincipalID.String() }

// inheritedMember is a membership that a space takes over from a space above.
type inheritedMember struct {
	spaces.Member
	From uuid.UUID // the space above that holds the entry
}

// inheritedMembers returns what the space takes over, nearest space first and every user or group once,
// with the entry of the nearest space. Whoever has an entry in the space itself (direct) is not listed
// again: that entry is the one that counts. members caches the memberships read so far.
func (s *Server) inheritedMembers(ctx context.Context, id uuid.UUID, direct []spaces.Member,
	links map[uuid.UUID]spaceLinkRow, members map[uuid.UUID][]spaces.Member) ([]inheritedMember, error) {
	seen := map[string]bool{}
	for _, m := range direct {
		seen[principalKey(m)] = true
	}
	var out []inheritedMember
	for _, up := range inheritedFrom(id, links) {
		ms, ok := members[up]
		if !ok {
			var err error
			if ms, err = s.Spaces.Members(ctx, up); err != nil {
				return nil, err
			}
			members[up] = ms
		}
		for _, m := range ms {
			if seen[principalKey(m)] {
				continue
			}
			seen[principalKey(m)] = true
			out = append(out, inheritedMember{Member: m, From: up})
		}
	}
	return out, nil
}

// canSee answers whether the caller may see a team space, which decides whether its name may be
// shown to them: whoever administers a space below it learns who has access to theirs, not what the
// space above is called.
func (s *Server) canSee(ctx context.Context, c *call) (func(uuid.UUID) bool, error) {
	if c.Actor.User.InstanceAdmin || s.Checker == nil {
		return func(uuid.UUID) bool { return true }, nil
	}
	roles, err := s.Checker.EffectiveRoles(ctx, c.Actor.User)
	if err != nil {
		return nil, err
	}
	return func(id uuid.UUID) bool { return roles.Role(id) > 0 }, nil
}
