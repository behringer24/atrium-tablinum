package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// registerCuratorTools adds what only curators (and admins) of a team space need. Like all
// tiers it is a convenience for the tool list; docs.Review re-checks the role per entry.
func (s *Server) registerCuratorTools(ms *mcp.Server) {
	addTool(s, ms, "review_knowledge", "Review draft", tierCurator, true, s.reviewKnowledge)
	s.registerCuratorSkillTools(ms)
	s.registerEditionTools(ms, tierCurator)
}

type reviewIn struct {
	ID       string `json:"id" jsonschema:"ID of the draft"`
	Decision string `json:"decision" jsonschema:"approve = approve, reject = return to the author with a note"`
	Note     string `json:"note,omitempty" jsonschema:"Note for the author; required when returning"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"For approve: true only after explicit confirmation by the user"`
}

type reviewOut struct {
	Message  string  `json:"message"`
	Document docInfo `json:"document"`
}

func (s *Server) reviewKnowledge(ctx context.Context, c *call, in reviewIn) (reviewOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return reviewOut{}, err
	}
	var approve bool
	switch strings.ToLower(strings.TrimSpace(in.Decision)) {
	case "approve":
		approve = true
	case "reject":
	default:
		return reviewOut{}, userErr("decision must be approve or reject.")
	}
	if approve && !in.Confirm {
		return reviewOut{}, userErr("Approval makes the entry visible to all readers of the space: present the draft to the user, ask for their confirmation and then call again with confirm=true.")
	}
	d, err := s.Docs.Review(ctx, c.Actor, docs.ReviewParams{ID: id, Approve: approve, Note: in.Note})
	if err != nil {
		return reviewOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return reviewOut{}, err
	}
	msg := "The draft has been approved and is now visible to all readers of the space."
	if !approve {
		msg = "The draft has been returned with your note. The author sees it on the entry and can revise it, after which it goes back into review."
	}
	return reviewOut{Message: msg, Document: info}, nil
}
