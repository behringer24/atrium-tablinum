package mcpserver

import (
	"context"
	"strings"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

var personalAliases = map[string]bool{
	// German and English spellings of the personal space.
	"personal": true, "persönlich": true, "persönlicher bereich": true, "persoenlich": true,
	"privat": true, "mein": true, "mein bereich": true, "mein persönlicher bereich": true,
	"mein privater bereich": true, "me": true, "my": true,
	"personal space": true, "my personal space": true, "my space": true,
	"private": true, "private space": true, "my private space": true,
}

// matchSpace resolves a user-supplied reference (slug, name, "mein persönlicher Bereich" / "my personal space"
// or a fragment) against candidate spaces. Exactly one hit returns it; several hits
// return them as candidates; none returns neither.
func matchSpace(list []spaces.Space, ref string) (*spaces.Space, []spaces.Space) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return nil, nil
	}
	if personalAliases[ref] {
		for i := range list {
			if list[i].Type == "personal" {
				return &list[i], nil
			}
		}
	}
	// The slug is unique and spaceInfos hands it out, so it also identifies a personal space.
	for i := range list {
		if list[i].Type == "personal" && strings.ToLower(list[i].Slug) == ref {
			return &list[i], nil
		}
	}
	tiers := []func(sp spaces.Space) bool{
		func(sp spaces.Space) bool { return strings.ToLower(sp.Slug) == ref },
		func(sp spaces.Space) bool { return strings.ToLower(sp.Name) == ref },
		func(sp spaces.Space) bool {
			return strings.Contains(strings.ToLower(sp.Name), ref) || strings.Contains(strings.ToLower(sp.Slug), ref)
		},
	}
	for _, match := range tiers {
		var hits []spaces.Space
		for _, sp := range list {
			if sp.Type == "team" && match(sp) { // personal spaces only via alias
				hits = append(hits, sp)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return &hits[0], nil
		default:
			return nil, hits
		}
	}
	return nil, nil
}

// spaceDisplayName is the name shown to assistants; personal spaces carry the owner's name
// internally.
func spaceDisplayName(sp spaces.Space) string {
	if sp.Type == "personal" {
		return "My personal space"
	}
	return sp.Name
}

// spacesAtLeast loads the spaces where the caller holds at least min.
func (s *Server) spacesAtLeast(ctx context.Context, a docs.Actor, min authz.Role) ([]spaces.Space, error) {
	return s.Spaces.ListByIDs(ctx, a.Roles.Spaces(min))
}
