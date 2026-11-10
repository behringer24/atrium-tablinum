package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The skills shipped in skills/ name tools (`store_knowledge`) and parameters (`draft=true`) of
// this build. A renamed tool or parameter has to fail here, not in a customer's chat.
func TestShippedSkillsNameRealTools(t *testing.T) {
	s := New(Deps{}, Settings{ProductName: "Atrium Tablinum", ShortName: "Atrium", OrganizationName: "Test", HandoffTTLDays: 14})
	ms := mcp.NewServer(&mcp.Implementation{Name: "atrium"}, nil)
	s.registerUserTools(ms)
	s.registerCuratorTools(ms, true)
	s.registerSpaceAdminTools(ms, true)
	s.registerInstanceAdminTools(ms)
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := ms.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]map[string]bool{} // tool -> its parameters
	for _, tl := range res.Tools {
		b, _ := json.Marshal(tl.InputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		params[tl.Name] = map[string]bool{}
		for p := range schema.Properties {
			params[tl.Name][p] = true
		}
	}

	code := regexp.MustCompile("`([^`\n]+)`")
	toolName := regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	argument := regexp.MustCompile(`^([a-z_]+)=`)
	root := filepath.Join("..", "..", "skills")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		var text strings.Builder
		err := filepath.Walk(filepath.Join(root, d.Name()), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Ext(p) != ".md" {
				return err
			}
			b, err := os.ReadFile(p)
			text.Write(b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		named := map[string]bool{}
		var args []string
		for _, m := range code.FindAllStringSubmatch(text.String(), -1) {
			switch tok := m[1]; {
			case toolName.MatchString(tok):
				if params[tok] == nil {
					t.Errorf("%s names the tool %s, which this build does not have", d.Name(), tok)
				}
				named[tok] = true
			case argument.MatchString(tok):
				args = append(args, argument.FindStringSubmatch(tok)[1])
			}
		}
		if len(named) == 0 {
			t.Errorf("%s names no tool: write tool names in backticks", d.Name())
		}
		for _, a := range args {
			found := false
			for tl := range named {
				found = found || params[tl][a]
			}
			if !found {
				t.Errorf("%s uses the parameter %s, which none of the tools it names has", d.Name(), a)
			}
		}
		n++
	}
	if n == 0 {
		t.Fatal("no shipped skills found")
	}
}
