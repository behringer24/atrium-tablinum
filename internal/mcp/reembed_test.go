package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/reembed"
)

// While the embedding service serves another model, saving fails with a message that says
// whom to tell, not with "internal error"; the model names stay in the log and admin_health.
func TestModelMismatchIsExplained(t *testing.T) {
	err := translate("store_knowledge", fmt.Errorf("embedding: %w: TEI serves %q", embed.ErrModelMismatch, "other/model"))
	if msg := err.Error(); !strings.Contains(msg, "instance admin") || strings.Contains(msg, "other/model") {
		t.Fatalf("message: %q", msg)
	}
}

// After a change of the model, vectors of the old one are left out of vector search and the
// duplicate check until admin_reembed has rebuilt them.
func TestAdminReembed(t *testing.T) {
	e := setup(t, 0)
	e.srv.Reembed = reembed.New(e.srv.Pool, fakeEmbedder{})
	boss, anna := e.session(t, "boss@x.de"), e.session(t, "anna@x.de")

	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Alpha one", "content": "alpha one"})
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Beta", "content": "beta two"})
	status := func() map[string]any {
		t.Helper()
		return e.mustCall(t, boss, "admin_reembed", nil)
	}
	if st := status(); st["stale"] != float64(0) || st["sections"].(float64) < 2 {
		t.Fatalf("fresh: %v", st)
	}

	// as if the configuration had named another model: the stored vectors are now foreign
	if _, err := e.srv.Pool.Exec(e.ctx, `UPDATE chunks SET embedding_model = 'old-model'`); err != nil {
		t.Fatal(err)
	}
	st := status()
	if st["stale"] != st["sections"] || st["by_model"].(map[string]any)["old-model"] != st["sections"] {
		t.Fatalf("stale: %v", st)
	}
	// a foreign vector does not count as a duplicate
	if out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Alpha two", "content": "alpha two"}); out["result"] != "stored" {
		t.Fatalf("compared with a vector of another model: %v", out)
	}

	if res, err := anna.CallTool(e.ctx, &mcp.CallToolParams{Name: "admin_reembed"}); err == nil && !res.IsError {
		t.Fatal("a user used admin_reembed")
	}
	if _, msg := e.call(t, boss, "admin_reembed", map[string]any{"action": "start"}); !strings.Contains(msg, "confirm") || !strings.Contains(msg, "2 sections") {
		t.Errorf("start without confirmation: %q", msg)
	}
	if st := e.mustCall(t, boss, "admin_reembed", map[string]any{"action": "start", "confirm": true}); st["requested_since"] == "" {
		t.Fatalf("start: %v", st)
	}
	// the periodic task: small batches, then done, and the request is withdrawn
	e.srv.Reembed.Batch = 1
	if err := e.srv.Reembed.Run(e.ctx); err != nil {
		t.Fatal(err)
	}
	if st := status(); st["stale"] != float64(0) || st["requested_since"] != nil {
		t.Fatalf("after the rebuild: %v", st)
	}
	// rebuilt vectors are compared again
	if out := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Alpha three", "content": "alpha three"}); out["result"] != "similar_found" {
		t.Errorf("duplicate check after the rebuild: %v", out)
	}
	if st := e.mustCall(t, boss, "admin_reembed", map[string]any{"action": "stop", "confirm": true}); !strings.Contains(st["message"].(string), "No re-embedding") {
		t.Errorf("stop without a rebuild: %v", st)
	}
	var n int
	if err := e.srv.Pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = 'reembed_start'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit: %d %v", n, err)
	}
}
