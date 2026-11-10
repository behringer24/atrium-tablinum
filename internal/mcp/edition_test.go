package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

type noIn struct{}
type noOut struct {
	Message string `json:"message"`
}

func noop(context.Context, *call, noIn) (noOut, error) { return noOut{}, nil }

// The curator and space admin tools of the core are listed for everyone, but a tool that an edition
// adds to such a group knows nothing of the compact form: it must appear only for the holders of the
// role, as before. (Such a tool used to show up in the list of every user.)
func TestEditionToolsAreOfferedToTheHoldersOfTheirGroup(t *testing.T) {
	saved := editionTools
	t.Cleanup(func() { editionTools = saved })
	editionTools = append(editionTools[:len(editionTools):len(editionTools)], func(s *Server, ms *mcp.Server, group tier) {
		switch group {
		case tierUser:
			addTool(s, ms, "ed_user", "Edition user tool", tierUser, false, noop)
		case tierCurator:
			addTool(s, ms, "ed_curator", "Edition curator tool", tierCurator, false, noop)
		case tierSpaceAdmin:
			addTool(s, ms, "ed_spaceadmin", "Edition space admin tool", tierSpaceAdmin, false, noop)
		case tierInstanceAdmin:
			addTool(s, ms, "ed_instanceadmin", "Edition instance admin tool", tierInstanceAdmin, false, noop)
		}
	})

	e := setup(t, 0)
	e.team(t, "support", "Support", false, map[string]authz.Role{"author@x.de": authz.Author, "cur@x.de": authz.Curator, "spaceadmin@x.de": authz.Admin})

	cases := []struct {
		who  string
		want map[string]bool
	}{
		{"author@x.de", map[string]bool{"ed_user": true}},
		{"cur@x.de", map[string]bool{"ed_user": true, "ed_curator": true}},
		{"spaceadmin@x.de", map[string]bool{"ed_user": true, "ed_curator": true, "ed_spaceadmin": true}},
		{"boss@x.de", map[string]bool{"ed_user": true, "ed_curator": true, "ed_spaceadmin": true, "ed_instanceadmin": true}},
	}
	for _, c := range cases {
		got := toolNames(t, e, e.session(t, c.who))
		for _, name := range []string{"ed_user", "ed_curator", "ed_spaceadmin", "ed_instanceadmin"} {
			if got[name] != c.want[name] {
				t.Errorf("%s: %s listed = %v, want %v", c.who, name, got[name], c.want[name])
			}
		}
	}
}
