package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/quota"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

type quotasIn struct {
	Action        string `json:"action" jsonschema:"get or set"`
	Scope         string `json:"scope,omitempty" jsonschema:"personal_default (all personal spaces), team_default (all team spaces), space (one team space) or user (the personal space of one user). get without a scope shows both defaults"`
	Space         string `json:"space,omitempty" jsonschema:"For scope space: the team space (slug or name)"`
	UserEmail     string `json:"user_email,omitempty" jsonschema:"For scope user: e-mail of the user"`
	Documents     *int64 `json:"documents,omitempty" jsonschema:"set: knowledge entries in the space; 0 = unlimited, -1 = remove the override (inherit again)"`
	Bytes         *int64 `json:"bytes,omitempty" jsonschema:"set: bytes of entries and handoffs in the space; 0 = unlimited, -1 = inherit"`
	Skills        *int64 `json:"skills,omitempty" jsonschema:"set: skills in the space; 0 = unlimited, -1 = inherit"`
	SkillBytes    *int64 `json:"skill_bytes,omitempty" jsonschema:"set: bytes of skill files in the space over all versions; 0 = unlimited, -1 = inherit"`
	SkillVersions *int64 `json:"skill_versions,omitempty" jsonschema:"set: versions of one skill; 0 = unlimited, -1 = inherit"`
	Confirm       bool   `json:"confirm,omitempty" jsonschema:"set: true only after confirmation by the user, who was told the new values"`
}

// quotaLimit is one limit with its source and, for a concrete space, the current usage.
type quotaLimit struct {
	Limit  int64  `json:"limit"`          // 0 = unlimited
	Source string `json:"source"`         // user, space, runtime default or config
	Used   *int64 `json:"used,omitempty"` // only for a concrete space
}

type quotaTarget struct {
	Scope     string                `json:"scope"`
	Target    string                `json:"target"`
	Limits    map[string]quotaLimit `json:"limits"`
	Overrides map[string]int64      `json:"overrides,omitempty"` // values set at exactly this scope
}

type quotasOut struct {
	Message string        `json:"message,omitempty"`
	Targets []quotaTarget `json:"targets"`
}

func limitValues(r quota.Resolved) map[string]int64 {
	return map[string]int64{"documents": int64(r.Documents), "bytes": r.Bytes, "skills": int64(r.Skills),
		"skill_bytes": r.SkillBytes, "skill_versions": int64(r.SkillVersions)}
}

func usageValues(u quota.Usage) map[string]int64 {
	return map[string]int64{"documents": int64(u.Documents), "bytes": u.Bytes, "skills": int64(u.Skills), "skill_bytes": u.SkillBytes}
}

func overridesOf(o quota.Overrides) map[string]int64 {
	out := map[string]int64{}
	for _, f := range quota.Fields {
		if v := o.Get(f); v != nil {
			out[f] = *v
		}
	}
	return out
}

// quotaTargetFor describes the limits of a scope. space is set for a concrete space (then the
// usage is shown too); raw are the overrides stored at exactly this scope.
func (s *Server) quotaTargetFor(ctx context.Context, scope quota.Scope, label string, sp *spaces.Space, raw quota.Overrides) (quotaTarget, error) {
	var r quota.Resolved
	var err error
	if sp != nil {
		r, err = s.Quota.Resolve(ctx, *sp)
	} else {
		r, err = s.Quota.ResolveDefault(ctx, scope)
	}
	if err != nil {
		return quotaTarget{}, err
	}
	t := quotaTarget{Scope: string(scope), Target: label, Limits: map[string]quotaLimit{}, Overrides: overridesOf(raw)}
	var used map[string]int64
	if sp != nil {
		u, err := s.Quota.Usage(ctx, sp.ID)
		if err != nil {
			return quotaTarget{}, err
		}
		used = usageValues(u)
	}
	for f, v := range limitValues(r) {
		l := quotaLimit{Limit: v, Source: r.Source[f]}
		if u, ok := used[f]; ok {
			l.Used = &u
		}
		t.Limits[f] = l
	}
	return t, nil
}

// resolveQuotaScope turns the arguments into a scope, the id of its override row and, for a
// concrete space, the space the usage is counted in.
func (s *Server) resolveQuotaScope(ctx context.Context, in quotasIn) (quota.Scope, uuid.UUID, string, *spaces.Space, error) {
	switch quota.Scope(strings.ToLower(strings.TrimSpace(in.Scope))) {
	case quota.PersonalDefault:
		return quota.PersonalDefault, uuid.Nil, "all personal spaces", nil, nil
	case quota.TeamDefault:
		return quota.TeamDefault, uuid.Nil, "all team spaces", nil, nil
	case quota.Space:
		all, err := s.Spaces.ListAllTeam(ctx)
		if err != nil {
			return "", uuid.Nil, "", nil, err
		}
		sp, cands := matchSpace(all, in.Space)
		switch {
		case sp != nil:
			return quota.Space, sp.ID, "the space " + sp.Name, sp, nil
		case len(cands) > 1:
			return "", uuid.Nil, "", nil, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", in.Space, spaceNames(cands)))
		}
		return "", uuid.Nil, "", nil, docs.ErrNotFound
	case quota.User:
		u, err := s.knownUser(ctx, in.UserEmail)
		if err != nil {
			return "", uuid.Nil, "", nil, err
		}
		if u.PersonalSpaceID == nil {
			return "", uuid.Nil, "", nil, userErr(fmt.Sprintf("%s has no personal space (it was deleted or converted).", u.Email))
		}
		sp, err := s.Spaces.GetByID(ctx, *u.PersonalSpaceID)
		if err != nil {
			return "", uuid.Nil, "", nil, err
		}
		return quota.User, u.ID, "the personal space of " + u.Email, &sp, nil
	}
	return "", uuid.Nil, "", nil, userErr("scope must be personal_default, team_default, space or user.")
}

func (s *Server) adminQuotas(ctx context.Context, c *call, in quotasIn) (quotasOut, error) {
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "get":
		return s.quotasGet(ctx, in)
	case "set":
		return s.quotasSet(ctx, c, in)
	}
	return quotasOut{}, userErr("action must be get or set.")
}

func (s *Server) quotasGet(ctx context.Context, in quotasIn) (quotasOut, error) {
	if strings.TrimSpace(in.Scope) == "" {
		out := quotasOut{}
		for _, d := range []struct {
			scope quota.Scope
			label string
		}{{quota.PersonalDefault, "all personal spaces"}, {quota.TeamDefault, "all team spaces"}} {
			raw, _, err := s.Quota.Raw(ctx, d.scope, uuid.Nil)
			if err != nil {
				return quotasOut{}, err
			}
			t, err := s.quotaTargetFor(ctx, d.scope, d.label, nil, raw)
			if err != nil {
				return quotasOut{}, err
			}
			out.Targets = append(out.Targets, t)
		}
		out.Message = "Defaults: a limit of 0 means unlimited. 'source' says where a value comes from: config (config.yaml) or runtime default (set with admin_quotas). A user or a space can have its own values."
		return out, nil
	}
	scope, id, label, sp, err := s.resolveQuotaScope(ctx, in)
	if err != nil {
		return quotasOut{}, err
	}
	raw, _, err := s.Quota.Raw(ctx, scope, id)
	if err != nil {
		return quotasOut{}, err
	}
	t, err := s.quotaTargetFor(ctx, scope, label, sp, raw)
	if err != nil {
		return quotasOut{}, err
	}
	return quotasOut{Targets: []quotaTarget{t}}, nil
}

func (s *Server) quotasSet(ctx context.Context, c *call, in quotasIn) (quotasOut, error) {
	patch := quota.Patch{Documents: in.Documents, Bytes: in.Bytes, Skills: in.Skills, SkillBytes: in.SkillBytes, SkillVersions: in.SkillVersions}
	if patch == (quota.Patch{}) {
		return quotasOut{}, userErr("Give at least one of documents, bytes, skills, skill_bytes or skill_versions (0 = unlimited, -1 = inherit again).")
	}
	scope, id, label, sp, err := s.resolveQuotaScope(ctx, in)
	if err != nil {
		return quotasOut{}, err
	}
	if !in.Confirm {
		return quotasOut{}, userErr(fmt.Sprintf("Changing the limits of %s applies to the next write, without a restart. Tell the user the new values and ask for confirmation, then call again with confirm=true.", label))
	}
	before, after, err := s.Quota.Set(ctx, scope, id, patch, c.Actor.User.ID)
	if err != nil {
		return quotasOut{}, userErr(err.Error())
	}
	changes := map[string]any{}
	for _, f := range quota.Fields {
		if patch.Get(f) == nil {
			continue
		}
		changes[f] = map[string]any{"from": before.Get(f), "to": after.Get(f)}
	}
	var team *spaces.Space
	if scope == quota.Space {
		team = sp
	}
	s.record(ctx, c.Actor, "admin_quota_set", team, map[string]any{"scope": string(scope), "target": label, "changes": changes})
	t, err := s.quotaTargetFor(ctx, scope, label, sp, after)
	if err != nil {
		return quotasOut{}, err
	}
	return quotasOut{Message: fmt.Sprintf("The limits of %s are updated and apply to the next write.", label), Targets: []quotaTarget{t}}, nil
}
