package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

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
	// From names the space above that the role is inherited from; empty for a membership of the space itself.
	From string `json:"from,omitempty"`
}

type rightsSpace struct {
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Archived         bool           `json:"archived,omitempty"`
	RequiresApproval bool           `json:"requires_approval"`
	DefaultRole      string         `json:"default_role"` // reader | author: everybody logged in; none: members only
	OpenToEveryone   bool           `json:"open_to_everyone"`
	Holders          []rightsHolder `json:"holders"`
	// The three fields below appear only for a space below another one.
	Parent           string         `json:"parent,omitempty"`
	InheritMembers   *bool          `json:"inherit_members,omitempty"`
	InheritedHolders []rightsHolder `json:"inherited_holders,omitempty"`
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
	members := make(map[uuid.UUID][]spaces.Member, len(all))
	bySpace := make(map[uuid.UUID]spaces.Space, len(all))
	for _, sp := range all {
		ms, err := s.Spaces.Members(ctx, sp.ID)
		if err != nil {
			return rightsOut{}, err
		}
		members[sp.ID] = ms
		bySpace[sp.ID] = sp
	}
	links, err := s.spaceLinks(ctx)
	if err != nil {
		return rightsOut{}, err
	}
	for _, link := range links {
		if link.parent != nil {
			out.Note += rightsHierarchyNote
			break
		}
	}
	for _, sp := range all {
		ms := members[sp.ID]
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
		var inheritedAdmins []string
		if link := links[sp.ID]; link.parent != nil {
			rs.Parent = bySpace[*link.parent].Slug
			inherit := link.inherit
			rs.InheritMembers = &inherit
			rs.InheritedHolders = inheritedHolders(sp.ID, ms, links, bySpace, members, groupSize)
			for _, h := range rs.InheritedHolders {
				if h.Role == authz.Admin.String() && !h.Former {
					admins++
					groupAdmin = groupAdmin || h.Type == spaces.PrincipalGroup
					inheritedAdmins = append(inheritedAdmins, h.Name+" ("+h.From+")")
				}
			}
		}
		out.Spaces = append(out.Spaces, rs)
		if sp.Archived {
			continue
		}
		if len(inheritedAdmins) > 0 {
			out.Findings = append(out.Findings, fmt.Sprintf("%s: the admin role of %s is inherited from the space above; inherit_members=false would stop that.", sp.Slug, strings.Join(inheritedAdmins, ", ")))
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

// rightsHierarchyNote is added to the note once some space sits below another one.
const rightsHierarchyNote = " A space below another one also inherits the memberships of the spaces above it unless inherit_members is false: " +
	"the nearest space with an entry for a user decides, and the inherited holders are listed with the space they come from. " +
	"A user with an entry of their own in a nearer space has that one instead, also when a group they belong to holds another role further up."

// spaceLinkRow is the place of a team space in the hierarchy.
type spaceLinkRow struct {
	parent  *uuid.UUID
	inherit bool
}

// spaceLinks reads the parent and the inheritance switch of every team space.
func (s *Server) spaceLinks(ctx context.Context) (map[uuid.UUID]spaceLinkRow, error) {
	out := map[uuid.UUID]spaceLinkRow{}
	if s.Pool == nil {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, parent_id, inherit_members FROM spaces WHERE type = 'team'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			l  spaceLinkRow
		)
		if err := rows.Scan(&id, &l.parent, &l.inherit); err != nil {
			return nil, err
		}
		out[id] = l
	}
	return out, rows.Err()
}

// inheritedHolders lists what a space takes over from the spaces above it, nearest first: walking up
// while the inheritance is on, every principal once, with the nearest entry of that principal. Whoever has a
// membership of their own in the space itself is not listed again.
func inheritedHolders(id uuid.UUID, direct []spaces.Member, links map[uuid.UUID]spaceLinkRow, bySpace map[uuid.UUID]spaces.Space,
	members map[uuid.UUID][]spaces.Member, groupSize map[string]int) []rightsHolder {
	key := func(m spaces.Member) string { return m.PrincipalType + ":" + m.PrincipalID.String() }
	seen := map[string]bool{}
	for _, m := range direct {
		seen[key(m)] = true
	}
	var out []rightsHolder
	cur := id
	for range len(links) + 1 { // a loop in the data ends the walk
		l := links[cur]
		if l.parent == nil || !l.inherit {
			break
		}
		cur = *l.parent
		up, ok := bySpace[cur]
		if !ok {
			break
		}
		for _, m := range members[cur] {
			if seen[key(m)] {
				continue
			}
			seen[key(m)] = true
			h := rightsHolder{Type: m.PrincipalType, Name: m.Name, Role: m.Role.String(), Former: m.Former, From: up.Slug}
			if m.PrincipalType == spaces.PrincipalGroup {
				h.Members = groupSize[m.Name]
			}
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return roleRank(out[i].Role) > roleRank(out[j].Role) })
	return out
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
