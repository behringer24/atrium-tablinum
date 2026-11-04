package mcpserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

// rpc posts one JSON-RPC request to /mcp as the given user and returns the decoded answer.
func (e *env) rpc(t *testing.T, email, method string, params any) map[string]any {
	t.Helper()
	token := "tok-" + email
	e.tokens[token] = email
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, e.http.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", method, res.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return m
}

func rpcCode(m map[string]any) float64 {
	er, _ := m["error"].(map[string]any)
	c, _ := er["code"].(float64)
	return c
}

func (e *env) exportGet(t *testing.T, email, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.export.URL+path, nil)
	if email != "" {
		e.tokens["tok-"+email] = email
		req.Header.Set("Authorization", "Bearer tok-"+email)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

// releaseSkill takes a skill through store, review and release as anna (author) and cora (curator).
func (e *env) releaseSkill(t *testing.T, anna, cora *mcp.ClientSession, name string, extra map[string]string) {
	t.Helper()
	e.mustCall(t, anna, "store_skill", map[string]any{"name": name, "space": "dev", "files": skillPackage(name, "Review a change before it is merged", extra)})
	e.mustCall(t, cora, "review_skill", map[string]any{"skill": name, "version": 1, "decision": "approve", "risk_class": "A", "confirm": true})
	e.mustCall(t, cora, "publish_skill", map[string]any{"skill": name, "version": 1, "confirm": true})
}

func TestSkillsExtension(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader,
	})
	e.team(t, "other", "Other", false, map[string]authz.Role{"dora@x.de": authz.Reader})
	anna, cora, bob, dora := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de"), e.session(t, "dora@x.de")

	checklist := "- tests pass\n- no secrets\n"
	files := skillPackage("code-review", "Review a change before it is merged", map[string]string{"references/checklist.md": checklist})
	e.mustCall(t, anna, "store_skill", map[string]any{"name": "code-review", "space": "dev", "files": files})

	// The extension is declared, together with the resources capability it needs.
	caps := bob.InitializeResult().Capabilities
	if _, ok := caps.Extensions[skillsExtension]; !ok || caps.Resources == nil {
		t.Fatalf("capabilities: %+v", caps)
	}

	// A draft is neither listed nor readable for a reader, by URI or by skills/get.
	uri := "skill://dev/code-review/SKILL.md"
	if list, _ := e.rpc(t, "bob@x.de", "skills/list", map[string]any{})["result"].(map[string]any); len(list["skills"].([]any)) != 0 {
		t.Fatalf("draft listed: %v", list)
	}
	if c := rpcCode(e.rpc(t, "bob@x.de", "skills/get", map[string]any{"uri": uri})); c != -32602 {
		t.Fatalf("draft by skills/get: code %v", c)
	}
	if _, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
		t.Fatal("reader read a draft")
	}

	e.mustCall(t, cora, "review_skill", map[string]any{"skill": "code-review", "version": 1, "decision": "approve", "risk_class": "A", "confirm": true})
	e.mustCall(t, cora, "publish_skill", map[string]any{"skill": "code-review", "version": 1, "confirm": true})

	// skills/list: verbatim front matter, complete resource list with digest and size.
	res := e.rpc(t, "bob@x.de", "skills/list", map[string]any{})["result"].(map[string]any)
	if res["resultType"] != "complete" {
		t.Fatalf("resultType: %v", res)
	}
	list := res["skills"].([]any)
	if len(list) != 1 {
		t.Fatalf("skills/list: %v", list)
	}
	entry := list[0].(map[string]any)
	if entry["uri"] != uri || entry["frontmatter"].(map[string]any)["name"] != "code-review" {
		t.Fatalf("entry: %v", entry)
	}
	sum := sha256.Sum256([]byte(checklist))
	var found bool
	for _, x := range entry["resources"].([]any) {
		r := x.(map[string]any)
		if r["uri"] == "skill://dev/code-review/references/checklist.md" {
			found = r["digest"] == "sha256:"+hex.EncodeToString(sum[:]) && int(r["size"].(float64)) == len(checklist)
		}
	}
	if !found || len(entry["resources"].([]any)) != 2 {
		t.Fatalf("resources: %v", entry["resources"])
	}

	// skills/get returns the same entry; unknown and foreign skills are the same error.
	got := e.rpc(t, "bob@x.de", "skills/get", map[string]any{"uri": uri})["result"].(map[string]any)["skill"].(map[string]any)
	if a, b := mustJSON(t, got), mustJSON(t, entry); a != b {
		t.Fatalf("skills/get differs from skills/list:\n%s\n%s", a, b)
	}
	for _, bad := range []string{"skill://dev/nope/SKILL.md", "skill://dev/code-review/references/checklist.md", "skill://nowhere/code-review/SKILL.md", "https://x/y", "skill://dev/code-review"} {
		if c := rpcCode(e.rpc(t, "bob@x.de", "skills/get", map[string]any{"uri": bad})); c != -32602 {
			t.Errorf("skills/get %q: code %v", bad, c)
		}
	}

	// Somebody without access to the space sees nothing.
	if l := e.rpc(t, "dora@x.de", "skills/list", map[string]any{})["result"].(map[string]any); len(l["skills"].([]any)) != 0 {
		t.Fatalf("outsider lists skills: %v", l)
	}
	if c := rpcCode(e.rpc(t, "dora@x.de", "skills/get", map[string]any{"uri": uri})); c != -32602 {
		t.Fatalf("outsider skills/get: code %v", c)
	}
	if _, err := dora.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
		t.Fatal("outsider read a skill")
	}

	// resources/read serves the files and nothing else.
	md, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil || len(md.Contents) != 1 || !strings.Contains(md.Contents[0].Text, "Run the tests") || md.Contents[0].MIMEType != "text/markdown" {
		t.Fatalf("read SKILL.md: %v %+v", err, md)
	}
	ref, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: "skill://dev/code-review/references/checklist.md"})
	if err != nil || ref.Contents[0].Text != checklist {
		t.Fatalf("read reference: %v %+v", err, ref)
	}
	for _, bad := range []string{"skill://dev/code-review/references/missing.md", "skill://dev/code-review/../x/SKILL.md", "skill://dev/code-review/"} {
		if _, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: bad}); err == nil {
			t.Errorf("read %q succeeded", bad)
		}
	}

	// A block takes effect at once on every route.
	e.mustCall(t, cora, "block_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "wrong advice", "confirm": true})
	if l := e.rpc(t, "bob@x.de", "skills/list", map[string]any{})["result"].(map[string]any); len(l["skills"].([]any)) != 0 {
		t.Fatalf("blocked skill listed: %v", l)
	}
	if _, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
		t.Fatal("blocked skill readable")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillsExtensionNeedsLogin(t *testing.T) {
	e := setup(t, 0)
	body := `{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{}}`
	res, err := http.Post(e.http.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestSkillExport(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader,
	})
	e.team(t, "other", "Other", false, map[string]authz.Role{"dora@x.de": authz.Reader})
	anna, cora := e.session(t, "anna@x.de"), e.session(t, "cora@x.de")
	e.releaseSkill(t, anna, cora, "code-review", map[string]string{"references/checklist.md": "- tests pass\n"})

	res, body := e.exportGet(t, "bob@x.de", "/skills/export/dev/code-review")
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" || res.Header.Get("X-Atrium-Skill-Version") != "1" || len(res.Header.Get("X-Atrium-Skill-Digest")) != 64 {
		t.Fatalf("export: %d %v", res.StatusCode, res.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "code-review/SKILL.md,code-review/references/checklist.md" {
		t.Fatalf("zip entries: %v", names)
	}
	// The same version always yields the same bytes, so a sync client can compare them.
	if _, again := e.exportGet(t, "bob@x.de", "/skills/export/dev/code-review?version=1"); !bytes.Equal(body, again) {
		t.Error("export is not reproducible")
	}

	for _, tc := range []struct {
		who, path string
		want      int
	}{
		{"", "/skills/export/dev/code-review", http.StatusUnauthorized},
		{"dora@x.de", "/skills/export/dev/code-review", http.StatusNotFound}, // no access to the space
		{"bob@x.de", "/skills/export/dev/missing", http.StatusNotFound},
		{"bob@x.de", "/skills/export/dev/code-review?version=2", http.StatusNotFound},
		{"bob@x.de", "/skills/export/dev/code-review?version=x", http.StatusBadRequest},
	} {
		if res, _ := e.exportGet(t, tc.who, tc.path); res.StatusCode != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.who, tc.path, res.StatusCode, tc.want)
		}
	}

	// A new draft cannot be exported, not even by the curator; a blocked version neither.
	e.mustCall(t, anna, "store_skill", map[string]any{"name": "code-review", "space": "dev", "files": skillPackage("code-review", "Second draft", nil)})
	if res, _ := e.exportGet(t, "cora@x.de", "/skills/export/dev/code-review?version=2"); res.StatusCode != http.StatusNotFound {
		t.Errorf("draft exported: %d", res.StatusCode)
	}
	e.mustCall(t, cora, "block_skill", map[string]any{"skill": "code-review", "version": 1, "reason": "wrong advice", "confirm": true})
	if res, _ := e.exportGet(t, "bob@x.de", "/skills/export/dev/code-review"); res.StatusCode != http.StatusNotFound {
		t.Errorf("blocked version exported: %d", res.StatusCode)
	}
}

// The audit log tells which route a client took: tool, resource read or skills/* method.
func TestSkillAuditNamesTheRoute(t *testing.T) {
	e := setup(t, 0)
	e.team(t, "dev", "Development", false, map[string]authz.Role{
		"anna@x.de": authz.Author, "cora@x.de": authz.Curator, "bob@x.de": authz.Reader,
	})
	anna, cora, bob := e.session(t, "anna@x.de"), e.session(t, "cora@x.de"), e.session(t, "bob@x.de")
	e.releaseSkill(t, anna, cora, "code-review", nil)

	uri := "skill://dev/code-review/SKILL.md"
	e.rpc(t, "bob@x.de", "skills/list", map[string]any{})
	e.rpc(t, "bob@x.de", "skills/get", map[string]any{"uri": uri})
	if _, err := bob.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	e.mustCall(t, bob, "get_skill", map[string]any{"skill": "code-review"})

	rows, err := e.srv.Pool.Query(e.ctx, `SELECT action, details->>'via' FROM audit_log
		WHERE action IN ('skill_list', 'skill_get') AND details ? 'via' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action, via string
		if err := rows.Scan(&action, &via); err != nil {
			t.Fatal(err)
		}
		got = append(got, action+"/"+via)
	}
	if want := "skill_list/rpc skill_get/rpc skill_get/resource skill_get/tool"; strings.Join(got, " ") != want {
		t.Errorf("audit routes: %v, want %s", got, want)
	}
}
