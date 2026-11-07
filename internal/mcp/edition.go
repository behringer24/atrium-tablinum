package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

// Hooks for the Pro build. The Pro files are laid over this package and
// fill these from init(); in the public build they stay empty and nothing changes.
var (
	// editionTools add tools to a server. They are called once for each group of tools that a session's role
	// opens (tierUser, tierCurator, tierSpaceAdmin, tierInstanceAdmin) and register only the tools of that
	// group, so a tool appears exactly for the roles that may call it. Each decides itself whether it
	// applies (for example by looking at s.Edition).
	editionTools []func(s *Server, ms *mcp.Server, group tier)
	// editionDescriptions are the default descriptions of those tools, rendered like the built-in ones
	// (templates, overrides from config).
	editionDescriptions = map[string]string{}
	// editionSupport add named parts to the support bundle.
	editionSupport []func(ctx context.Context, s *Server) (name string, part any, err error)
	// editionOffboarded run when admin_offboard_user has locked an account, and may add to the answer
	// (the guided offboarding of Pro adds counts and hints). The account is already locked then, so they
	// cannot fail the call.
	editionOffboarded []func(ctx context.Context, s *Server, u users.User, out *offboardOut)
)

// registerEditionTools lets the Pro build add the tools of one group.
func (s *Server) registerEditionTools(ms *mcp.Server, group tier) {
	for _, add := range editionTools {
		add(s, ms, group)
	}
}
