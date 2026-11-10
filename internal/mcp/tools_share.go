package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// registerShareTools adds the tools that send an entry or a skill to another user. They exist
// only where sharing is enabled; the stores refuse the call otherwise as well.
func (s *Server) registerShareTools(ms *mcp.Server) {
	if !s.set.SharingEnabled {
		return
	}
	addTool(s, ms, "share_knowledge", "Send entry to a colleague", tierUser, true, s.shareKnowledge)
	if s.Skills != nil {
		addTool(s, ms, "share_skill", "Send skill to a colleague", tierUser, true, s.shareSkill)
	}
}

type shareKnowledgeIn struct {
	ID        string `json:"id" jsonschema:"ID of the approved entry to send"`
	Recipient string `json:"recipient" jsonschema:"E-mail address of the colleague"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"true only after explicit confirmation by the user"`
}

type shareOut struct {
	Message string `json:"message"`
}

func (s *Server) shareKnowledge(ctx context.Context, c *call, in shareKnowledgeIn) (shareOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return shareOut{}, err
	}
	recipient := strings.TrimSpace(in.Recipient)
	if !in.Confirm {
		cur, err := s.Docs.Get(ctx, c.Actor, id)
		if err != nil {
			return shareOut{}, err
		}
		return shareOut{}, userErr(fmt.Sprintf("Sending \"%s\" to %s needs a confirmation. A copy lands in their personal space as a draft: only they can read it and decide whether to keep it, and you will not see what became of it. Binding rules arrive as plain documentation. Ask the user and then call again with confirm=true.", cur.Title, recipient))
	}
	if err := s.Docs.Share(ctx, c.Actor, docs.ShareParams{ID: id, Email: recipient}); err != nil {
		return shareOut{}, err
	}
	return shareOut{Message: fmt.Sprintf("A copy of the entry was sent to %s. It waits in their personal space until they review it; you will not be told their decision.", recipient)}, nil
}

type shareSkillIn struct {
	Skill     string `json:"skill" jsonschema:"Name of the skill (its newest released version is sent)"`
	Recipient string `json:"recipient" jsonschema:"E-mail address of the colleague"`
	Space     string `json:"space,omitempty" jsonschema:"Space the skill is in; needed only when several spaces have a skill of this name"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"true only after explicit confirmation by the user"`
}

func (s *Server) shareSkill(ctx context.Context, c *call, in shareSkillIn) (shareOut, error) {
	if s.Skills == nil {
		return shareOut{}, errNoSkills
	}
	recipient := strings.TrimSpace(in.Recipient)
	v, choice, err := s.skillOf(ctx, c, skillRefIn{Skill: in.Skill, Space: in.Space}, "")
	if err != nil {
		return shareOut{}, err
	}
	if choice != nil {
		return shareOut{}, userErr(choice.Message)
	}
	if !in.Confirm {
		return shareOut{}, userErr(fmt.Sprintf("Sending the skill %q to %s needs a confirmation. A copy of its newest released version lands in their personal space as a draft: they have to read, approve and release it before their assistant offers it, and you will not see what became of it. Ask the user and then call again with confirm=true.", v.Name, recipient))
	}
	if err := s.Skills.Share(ctx, c.Actor, v.SkillID, recipient); err != nil {
		return shareOut{}, err
	}
	return shareOut{Message: fmt.Sprintf("A copy of the skill %q was sent to %s. It waits in their personal space until they review and release it; you will not be told their decision.", v.Name, recipient)}, nil
}
