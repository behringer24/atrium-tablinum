package mcpserver

import (
	"strings"
	"testing"
)

// update_knowledge with expected_version: the update succeeds only if the entry is still at the
// version the caller loaded; otherwise nothing is saved and the answer names the current version.
func TestUpdateKnowledgeExpectedVersion(t *testing.T) {
	e := setup(t, 0)
	anna := e.session(t, "anna@x.de")

	id := e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "Lock", "content": "start"})["id"].(string)
	if v := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": id})["version"].(float64); v != 1 {
		t.Fatalf("version after store: %v", v)
	}

	// Two assistants load version 1; the first saves, the second must not overwrite it.
	up := e.mustCall(t, anna, "update_knowledge", map[string]any{"id": id, "content": "first", "expected_version": 1})
	if up["version"].(float64) != 2 {
		t.Fatalf("first update: %v", up)
	}
	_, msg := e.call(t, anna, "update_knowledge", map[string]any{"id": id, "content": "second", "expected_version": 1})
	for _, want := range []string{"version 2", "not 1", "Nothing was saved", "expected_version=2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("conflict message lacks %q: %q", want, msg)
		}
	}
	got := e.mustCall(t, anna, "get_knowledge", map[string]any{"id": id})
	if got["version"].(float64) != 2 || !strings.Contains(got["content"].(string), "first") {
		t.Errorf("the conflicting update changed the entry: %v", got)
	}

	// With the version from the answer it goes through.
	up = e.mustCall(t, anna, "update_knowledge", map[string]any{"id": id, "content": "merged", "expected_version": 2})
	if up["version"].(float64) != 3 {
		t.Errorf("update after reload: %v", up)
	}

	// A change of the responsible person or review date alone is checked as well.
	if _, msg := e.call(t, anna, "update_knowledge", map[string]any{"id": id, "review_due": "2027-01-01", "expected_version": 1}); !strings.Contains(msg, "version 3") {
		t.Errorf("meta update with a stale version: %q", msg)
	}

	// Without expected_version the behaviour is unchanged.
	if up := e.mustCall(t, anna, "update_knowledge", map[string]any{"id": id, "content": "unchecked"}); up["version"].(float64) != 4 {
		t.Errorf("update without expected_version: %v", up)
	}
}
