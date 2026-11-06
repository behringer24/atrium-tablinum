package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Hooks for the Pro build (docs/design/licensing.md). The Pro files are laid over this package and
// fill these from init(); in the public build they stay empty and nothing changes.
var (
	// editionTools add tools to a server. Each decides itself whether it applies (for example by looking
	// at s.Edition).
	editionTools []func(s *Server, ms *mcp.Server)
	// editionDescriptions are the default descriptions of those tools, rendered like the built-in ones
	// (templates, overrides from config).
	editionDescriptions = map[string]string{}
	// editionSupport add named parts to the support bundle.
	editionSupport []func(ctx context.Context, s *Server) (name string, part any, err error)
)
