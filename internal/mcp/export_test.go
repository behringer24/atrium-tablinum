package mcpserver

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// exportEnv serves the download route next to the MCP endpoint.
func exportEnv(t *testing.T) *env {
	t.Helper()
	e := setup(t, 0)
	mux := http.NewServeMux()
	mux.Handle("GET /export/{token}", e.srv.ExportHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	e.srv.set.BaseURL = ts.URL
	return e
}

// download opens a link and returns the status and, for 200, the files of the ZIP.
func download(t *testing.T, url string) (int, map[string]string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a ZIP: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	return resp.StatusCode, files
}

func (e *env) exportURL(t *testing.T, email string, args map[string]any) string {
	t.Helper()
	out := e.mustCall(t, e.session(t, email), "export_knowledge", args)
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, e.srv.set.BaseURL+"/export/") {
		t.Fatalf("link: %q", url)
	}
	return url
}

func anyFile(files map[string]string, prefix string) (string, string) {
	for name, content := range files {
		if strings.HasPrefix(name, prefix) && !strings.HasSuffix(name, "index.md") {
			return name, content
		}
	}
	return "", ""
}

func TestExportOfASpace(t *testing.T) {
	e := exportEnv(t)
	e.team(t, "support", "Support", false, map[string]authz.Role{"sadmin@x.de": authz.Admin, "author@x.de": authz.Author, "other@x.de": authz.Author})
	e.team(t, "dev", "Dev", false, map[string]authz.Role{"sadmin@x.de": authz.Reader, "author@x.de": authz.Author})
	author := e.session(t, "author@x.de")
	e.mustCall(t, author, "store_knowledge", map[string]any{"title": "Reset a password: \"quoted\" & <tagged>", "content": "Step one.\n\nStep two.", "space": "support", "tags": []string{"it", "howto"}, "review_due": "2030-01-15", "force": true})
	old := e.mustCall(t, author, "store_knowledge", map[string]any{"title": "Old thing", "content": "obsolete", "space": "support", "force": true})["id"].(string)
	e.mustCall(t, author, "archive_knowledge", map[string]any{"id": old, "confirm": true})
	e.mustCall(t, author, "store_knowledge", map[string]any{"title": "Other space", "content": "dev only", "space": "dev", "force": true})

	status, files := download(t, e.exportURL(t, "sadmin@x.de", map[string]any{"space": "support"}))
	if status != http.StatusOK {
		t.Fatalf("download: HTTP %d", status)
	}
	for name := range files {
		if !strings.HasPrefix(name, "support/") && name != "index.md" {
			t.Errorf("a file outside the exported space: %s", name)
		}
	}
	_, entry := anyFile(files, "support/reset-a-password")
	for _, want := range []string{"---\n", `title: "Reset a password: \"quoted\" & <tagged>"`, `tags: ["howto", "it"]`, "status: stable", `  status: "approved"`, `  author: "author@x.de"`, "Step one.\n\nStep two.\n",
		"stale_after: 2030-01-15T", "generated:\n  by: \"human:author@x.de\"\n  at: 20"} {
		if !strings.Contains(entry, want) {
			t.Errorf("entry lacks %q:\n%s", want, entry)
		}
	}
	_, archived := anyFile(files, "support/old-thing")
	if !strings.Contains(archived, "status: deprecated") || !strings.Contains(archived, `  status: "archived"`) {
		t.Errorf("an archived entry belongs in the export, marked as such:\n%s", archived)
	}
	if !strings.Contains(files["support/index.md"], "* [Reset a password") || strings.HasPrefix(files["support/index.md"], "---") {
		t.Errorf("space index: %q", files["support/index.md"])
	}
	if !strings.HasPrefix(files["index.md"], "---\nokf_version: \"0.2\"\n---\n") || !strings.Contains(files["index.md"], "* [Support](support/) - 2 entries") {
		t.Errorf("root index: %q", files["index.md"])
	}
	// The rules of the Open Knowledge Format for a bundle: every file but the indexes has a front
	// matter with a non-empty type, and an index outside the root has no front matter.
	typeLine := regexp.MustCompile(`(?m)^type: ".+"$`)
	for name, content := range files {
		if strings.HasSuffix(name, "index.md") {
			continue
		}
		front, _, ok := strings.Cut(strings.TrimPrefix(content, "---\n"), "\n---\n")
		if !strings.HasPrefix(content, "---\n") || !ok || !typeLine.MatchString(front) {
			t.Errorf("%s is not an OKF concept file:\n%s", name, content)
		}
	}

	var found bool
	for _, en := range e.mustCall(t, e.session(t, "boss@x.de"), "admin_audit_log", map[string]any{"action": "knowledge_export"})["entries"].([]any) {
		found = found || en.(map[string]any)["action"] == "knowledge_export"
	}
	if !found {
		t.Error("the export is not in the audit log")
	}
}

func TestExportRights(t *testing.T) {
	e := exportEnv(t)
	e.team(t, "support", "Support", false, map[string]authz.Role{"sadmin@x.de": authz.Admin, "cur@x.de": authz.Curator, "author@x.de": authz.Author})
	e.team(t, "dev", "Dev", false, map[string]authz.Role{"devadmin@x.de": authz.Admin})
	anna := e.session(t, "anna@x.de")

	// Only admins export a space; a curator, an author or a stranger does not even learn it exists.
	for _, who := range []string{"cur@x.de", "author@x.de", "anna@x.de"} {
		if _, msg := e.call(t, e.session(t, who), "export_knowledge", map[string]any{"space": "support"}); !strings.Contains(msg, "no space of that name") {
			t.Errorf("%s: %q", who, msg)
		}
	}
	// A space admin of another space gets no access to this one.
	if _, msg := e.call(t, e.session(t, "devadmin@x.de"), "export_knowledge", map[string]any{"space": "support"}); !strings.Contains(msg, "no space of that name") {
		t.Errorf("admin of another space: %q", msg)
	}
	// Without a name a user with no admin role has nothing to export.
	if _, msg := e.call(t, anna, "export_knowledge", map[string]any{}); !strings.Contains(msg, "administer no team space") {
		t.Errorf("no space: %q", msg)
	}

	// Everybody can take their own notes out.
	e.mustCall(t, anna, "store_knowledge", map[string]any{"title": "My note", "content": "private", "space": "my personal space", "force": true})
	status, files := download(t, e.exportURL(t, "anna@x.de", map[string]any{"space": "my personal space"}))
	if status != http.StatusOK {
		t.Fatalf("personal export: HTTP %d", status)
	}
	if name, content := anyFile(files, "personal-"); name == "" || !strings.Contains(content, "private") {
		t.Errorf("personal export: %v", files)
	}

	// An instance admin cannot reach a personal space, neither by name nor through the whole export.
	boss := e.session(t, "boss@x.de")
	au, err := e.users.GetByEmail(e.ctx, "anna@x.de")
	if err != nil || au.PersonalSpaceID == nil {
		t.Fatalf("anna has no personal space: %v", err)
	}
	asp, err := e.spaces.GetByID(e.ctx, *au.PersonalSpaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, msg := e.call(t, boss, "export_knowledge", map[string]any{"space": asp.Slug}); !strings.Contains(msg, "no space of that name") {
		t.Errorf("an instance admin reached a personal space: %q", msg)
	}
	status, all := download(t, e.exportURL(t, "boss@x.de", map[string]any{}))
	if status != http.StatusOK {
		t.Fatalf("whole export: HTTP %d", status)
	}
	var support, dev bool
	for name, content := range all {
		support = support || strings.HasPrefix(name, "support/")
		dev = dev || strings.HasPrefix(name, "dev/")
		if strings.HasPrefix(name, "personal-") || strings.Contains(content, "private") {
			t.Errorf("a personal space is part of the whole export: %s", name)
		}
	}
	if !support || !dev {
		t.Errorf("the whole export lacks a team space: support=%v dev=%v", support, dev)
	}
}

func TestExportLinkIsSingleUseShortLivedAndRechecked(t *testing.T) {
	e := exportEnv(t)
	sp := e.team(t, "support", "Support", false, map[string]authz.Role{"sadmin@x.de": authz.Admin})

	url := e.exportURL(t, "sadmin@x.de", map[string]any{"space": "support"})
	if status, _ := download(t, url); status != http.StatusOK {
		t.Fatalf("first download: HTTP %d", status)
	}
	if status, _ := download(t, url); status != http.StatusNotFound {
		t.Errorf("a link worked twice: HTTP %d", status)
	}

	// A forged or altered link works for nobody.
	fresh := e.exportURL(t, "sadmin@x.de", map[string]any{"space": "support"})
	if status, _ := download(t, fresh[:len(fresh)-2]+"xx"); status != http.StatusNotFound {
		t.Errorf("tampered link: HTTP %d", status)
	}
	if status, _ := download(t, e.srv.set.BaseURL+"/export/garbage"); status != http.StatusNotFound {
		t.Errorf("garbage: HTTP %d", status)
	}

	// An expired link is dead.
	admin, err := e.users.GetByEmail(e.ctx, "sadmin@x.de")
	if err != nil {
		t.Fatal(err)
	}
	stale := e.srv.set.BaseURL + "/export/" + e.srv.exports.issue(admin.ID, sp.ID.String(), time.Now().Add(-2*exportLinkTTL))
	if status, _ := download(t, stale); status != http.StatusNotFound {
		t.Errorf("expired link: HTTP %d", status)
	}

	// Rights are checked again at the download: whoever lost the role in the meantime gets nothing.
	again := e.exportURL(t, "sadmin@x.de", map[string]any{"space": "support"})
	if err := e.spaces.RemoveMember(e.ctx, sp.ID, spaces.PrincipalUser, admin.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := download(t, again); status != http.StatusNotFound {
		t.Errorf("link of a former admin: HTTP %d", status)
	}

	// So does a locked account.
	e.team(t, "dev", "Dev", false, map[string]authz.Role{"locked@x.de": authz.Admin})
	locked := e.exportURL(t, "locked@x.de", map[string]any{"space": "dev"})
	lu, _ := e.users.GetByEmail(e.ctx, "locked@x.de")
	if err := e.users.SetActive(e.ctx, lu.ID, false); err != nil {
		t.Fatal(err)
	}
	if status, _ := download(t, locked); status != http.StatusNotFound {
		t.Errorf("link of a locked account: HTTP %d", status)
	}
}
