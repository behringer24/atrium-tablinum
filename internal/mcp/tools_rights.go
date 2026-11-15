package mcpserver

import (
	"context"
	"fmt"
	"sort"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

type rightsIn struct{}

// rightsHolder is one user or group that holds a role in a space through a membership.
type rightsHolder struct {
	Type    string `json:"type"` // user | group
	Name    string `json:"name"` // e-mail address or group name
	Role    string `json:"role"`
	Members int    `json:"members,omitempty"` // groups: how many accounts the group has
	Former  bool   `json:"former,omitempty"`  // locked account: keeps the membership, has no rights
}

type rightsSpace struct {
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Archived         bool           `json:"archived,omitempty"`
	RequiresApproval bool           `json:"requires_approval"`
	DefaultRole      string         `json:"default_role"` // reader | author: everybody logged in; none: members only
	OpenToEveryone   bool           `json:"open_to_everyone"`
	Holders          []rightsHolder `json:"holders"`
}

type rightsOut struct {
	Note string `json:"note"`
	// InstanceAdmins are admin in every team space (see AdminsReadContent for the content).
	InstanceAdmins []instanceAdminInfo `json:"instance_admins"`
	// AdminsReadContent says whether instance admins also read the content of every team space.
	AdminsReadContent bool          `json:"admins_read_content"`
	Spaces            []rightsSpace `json:"spaces"`
	// Findings point at settings that an operator usually wants to look at; they are hints, not errors.
	Findings []string `json:"findings"`
}

const rightsNote = "Who holds which role where, read from the running configuration. Instance admins are admin in every team space on top of this list; " +
	"personal spaces belong to their owner alone and are not part of it. The effective role of a user is the highest of their memberships, " +
	"the memberships of their groups and the default role of the space."

// adminRightsReport shows the setup of rights of the whole instance in one place, so that an
// operator can check it without going through every space.
func (s *Server) adminRightsReport(ctx context.Context, _ *call, _ rightsIn) (rightsOut, error) {
	out := rightsOut{Note: rightsNote, Spaces: []rightsSpace{}, Findings: []string{}, AdminsReadContent: true}
	if s.Pool != nil {
		out.AdminsReadContent = authz.AdminsReadContent(ctx, s.Pool)
	}
	var err error
	if out.InstanceAdmins, err = s.instanceAdminList(ctx); err != nil {
		return rightsOut{}, err
	}
	groupSize := map[string]int{}
	if groups, err := s.Users.ListGroups(ctx); err == nil {
		for _, g := range groups {
			groupSize[g.Name] = g.MemberCount
		}
	} else {
		return rightsOut{}, err
	}
	all, err := s.Spaces.ListAllTeam(ctx)
	if err != nil {
		return rightsOut{}, err
	}
	for _, sp := range all {
		ms, err := s.Spaces.Members(ctx, sp.ID)
		if err != nil {
			return rightsOut{}, err
		}
		rs := rightsSpace{Slug: sp.Slug, Name: sp.Name, Archived: sp.Archived, RequiresApproval: sp.RequireApproval,
			DefaultRole: defaultRoleName(sp.DefaultRole), OpenToEveryone: sp.DefaultRole != authz.None, Holders: make([]rightsHolder, 0, len(ms))}
		admins, groupAdmin := 0, false
		for _, m := range ms {
			h := rightsHolder{Type: m.PrincipalType, Name: m.Name, Role: m.Role.String(), Former: m.Former}
			if m.PrincipalType == spaces.PrincipalGroup {
				h.Members = groupSize[m.Name]
			}
			if m.Role == authz.Admin && !m.Former {
				admins++
				groupAdmin = groupAdmin || m.PrincipalType == spaces.PrincipalGroup
			}
			rs.Holders = append(rs.Holders, h)
		}
		sort.SliceStable(rs.Holders, func(i, j int) bool { return roleRank(rs.Holders[i].Role) > roleRank(rs.Holders[j].Role) })
		out.Spaces = append(out.Spaces, rs)
		if sp.Archived {
			continue
		}
		switch {
		case admins == 0:
			out.Findings = append(out.Findings, fmt.Sprintf("%s: no space admin (only instance admins can manage it).", sp.Slug))
		case groupAdmin:
			out.Findings = append(out.Findings, fmt.Sprintf("%s: a group holds the admin role; everybody who joins that group becomes space admin.", sp.Slug))
		}
		if sp.DefaultRole == authz.Author {
			out.Findings = append(out.Findings, fmt.Sprintf("%s: every logged-in user may write here (default role author).", sp.Slug))
		} else if sp.DefaultRole == authz.Reader {
			out.Findings = append(out.Findings, fmt.Sprintf("%s: every logged-in user may read here (default role reader).", sp.Slug))
		}
	}
	if !out.AdminsReadContent {
		out.Findings = append(out.Findings, "Instance admins do not read team spaces they are not members of (content separation is on).")
	}
	return out, nil
}

func roleRank(role string) int {
	r, _ := authz.ParseRole(role)
	return int(r)
}

func defaultRoleName(r authz.Role) string {
	if r == authz.None {
		return "none"
	}
	return r.String()
}
