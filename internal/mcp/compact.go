package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolOpt adjusts a tool definition before it is registered.
type toolOpt func(*mcp.Tool)

// The tool list is read once by most clients and kept, so the tools of curators and space admins
// are listed for everyone. Whoever does not hold the role gets a compact form: a one-sentence
// description that names the role, and the parameter schema without the per-parameter texts. That
// keeps the cost of a longer list small. The full definition follows with the next connection
// after the role was granted; the role itself is checked on every call.
const (
	curatorHint    = " For curators: needs the curator or admin role in a team space, otherwise the call is refused."
	spaceAdminHint = " For space admins: needs the admin role in a team space, otherwise the call is refused."
)

var compactSummaries = map[string]string{
	"review_knowledge":    "Approves or returns a draft entry (decision approve or reject).",
	"review_skill":        "Approves or rejects a draft version of a skill after reading all its files (pass the digest from get_skill).",
	"publish_skill":       "Releases an approved skill version to the readers of the space.",
	"block_skill":         "Withdraws an approved or released skill version, for example when it turns out to be harmful.",
	"unblock_skill":       "Lifts the block of a skill version.",
	"space_members":       "Shows members and roles of a space the user manages.",
	"space_add_member":    "Adds a user or a group to a space the user manages.",
	"space_remove_member": "Removes a user or a group from a space the user manages.",
}

// reviewHint is for the review tools: they are also the owner's tools for what other users sent to
// their personal space.
const reviewHint = " For curators of a team space, and for what other users sent to your personal space: the role is checked per item, otherwise the call is refused."

var compactHints = map[string]string{
	"review_knowledge": reviewHint, "review_skill": reviewHint, "publish_skill": reviewHint,
	"unblock_skill": spaceAdminHint, "space_members": spaceAdminHint,
	"space_add_member": spaceAdminHint, "space_remove_member": spaceAdminHint,
}

// compactFor returns the option that turns a tool into its compact form, or nothing when the
// caller holds the role.
func (s *Server) compactFor(name string, member bool) toolOpt {
	if member {
		return func(*mcp.Tool) {}
	}
	return func(t *mcp.Tool) {
		summary, ok := compactSummaries[name]
		if !ok {
			return
		}
		hint := compactHints[name]
		if hint == "" {
			hint = curatorHint
		}
		t.Description = summary + hint
		if in, ok := t.InputSchema.(*jsonschema.Schema); ok {
			stripDescriptions(in)
		}
		if out, ok := t.OutputSchema.(*jsonschema.Schema); ok {
			stripDescriptions(out)
		}
	}
}

// stripDescriptions removes the texts of the parameters, keeping names, types and the list of
// required ones.
func stripDescriptions(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	for _, p := range s.Properties {
		p.Description = ""
		stripDescriptions(p)
	}
	for _, d := range s.Defs {
		stripDescriptions(d)
	}
	stripDescriptions(s.Items)
}
