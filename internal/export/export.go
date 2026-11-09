// Package export writes the knowledge of spaces as a ZIP archive of Markdown files: one folder
// per space, one file per entry with a YAML front matter, and an index.md per folder.
//
// The archive is an Open Knowledge Format (OKF) bundle, version 0.2: every entry has a YAML
// front matter with the field names of the format (type, title, tags, status, stale_after,
// generated), the index.md files follow its list format and the root index.md names the
// version. Everything else Atrium knows about the entry is kept under the key atrium, so that
// nothing is lost; the format allows additional keys. The rights are not decided here: the caller passes
// an actor, and docs.Store.ExportSpace lets only admins of a space through.
package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// SpaceCount is the number of entries written for one space.
type SpaceCount struct {
	Space   spaces.Space
	Entries int
}

// Result tells what an export contained.
type Result struct {
	Spaces  []SpaceCount
	Entries int
}

// Write streams the given spaces to w as a ZIP archive. Whether the actor may export a space
// is checked per space by the document store. If an error occurs part-way, the archive is left
// without its end record and so does not open: a partial export must never look complete.
func Write(ctx context.Context, w io.Writer, store *docs.Store, a docs.Actor, list []spaces.Space, now time.Time) (Result, error) {
	zw := zip.NewWriter(w)
	var res Result
	for _, sp := range list {
		var lines []string
		used := map[string]bool{}
		count := 0
		err := store.ExportSpace(ctx, a, sp.ID, func(d docs.Document) error {
			name := fileName(d.Title, d.ID, used)
			fh := &zip.FileHeader{Name: sp.Slug + "/" + name, Method: zip.Deflate, Modified: d.UpdatedAt}
			fw, err := zw.CreateHeader(fh)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(fw, entryFile(sp, d)); err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("* [%s](%s) - %s, %s", linkText(d.Title), name, typeOf(d), d.Status))
			count++
			return nil
		})
		if err != nil {
			return res, err
		}
		if err := writeFile(zw, sp.Slug+"/index.md", now, spaceIndex(sp, lines)); err != nil {
			return res, err
		}
		res.Spaces = append(res.Spaces, SpaceCount{Space: sp, Entries: count})
		res.Entries += count
	}
	if err := writeFile(zw, "index.md", now, rootIndex(res, now)); err != nil {
		return res, err
	}
	return res, zw.Close()
}

func writeFile(zw *zip.Writer, name string, mod time.Time, content string) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod})
	if err != nil {
		return err
	}
	_, err = io.WriteString(fw, content)
	return err
}

func typeOf(d docs.Document) string {
	if d.Type == "" {
		return docs.TypeDocumentation
	}
	return d.Type
}

// okfStatus maps the state of an entry to the three values of the Open Knowledge Format; the
// exact state stays in atrium.status.
func okfStatus(status string) string {
	switch status {
	case "approved":
		return "stable"
	case "archived":
		return "deprecated"
	}
	return "draft"
}

// entryFile is one Markdown file: front matter, an empty line, the content.
func entryFile(sp spaces.Space, d docs.Document) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", quote(typeOf(d)))
	fmt.Fprintf(&b, "title: %s\n", quote(d.Title))
	tags := make([]string, 0, len(d.Tags))
	for _, t := range d.Tags {
		tags = append(tags, quote(t))
	}
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(tags, ", "))
	fmt.Fprintf(&b, "status: %s\n", okfStatus(d.Status))
	if d.ReviewDue != nil {
		// A datetime, not a string, as the format wants it.
		fmt.Fprintf(&b, "stale_after: %s\n", d.ReviewDue.UTC().Format(time.RFC3339))
	}
	// Atrium files an entry only when a person asks for it, so the author is the actor.
	b.WriteString("generated:\n")
	fmt.Fprintf(&b, "  by: %s\n", quote("human:"+d.AuthorEmail))
	fmt.Fprintf(&b, "  at: %s\n", d.UpdatedAt.UTC().Format(time.RFC3339))
	b.WriteString("atrium:\n")
	fmt.Fprintf(&b, "  id: %s\n", quote(d.ID.String()))
	fmt.Fprintf(&b, "  space: %s\n", quote(sp.Slug))
	fmt.Fprintf(&b, "  status: %s\n", quote(d.Status))
	fmt.Fprintf(&b, "  version: %d\n", d.Version)
	fmt.Fprintf(&b, "  author: %s\n", quote(d.AuthorEmail))
	if d.OwnerEmail != "" {
		fmt.Fprintf(&b, "  owner: %s\n", quote(d.OwnerEmail))
	}
	if d.Source != "" {
		fmt.Fprintf(&b, "  source: %s\n", quote(d.Source))
	}
	fmt.Fprintf(&b, "  created: %s\n", quote(d.CreatedAt.UTC().Format(time.RFC3339)))
	fmt.Fprintf(&b, "  updated: %s\n", quote(d.UpdatedAt.UTC().Format(time.RFC3339)))
	if d.ReviewNote != "" {
		fmt.Fprintf(&b, "  review_note: %s\n", quote(d.ReviewNote))
	}
	b.WriteString("---\n\n")
	b.WriteString(d.Content)
	if !strings.HasSuffix(d.Content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// quote writes s as a YAML double-quoted scalar. JSON strings are valid YAML double-quoted
// scalars, so every character, including line breaks and quotes, is safe.
func quote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// fileName makes a readable, safe file name from the title. The start of the id makes it
// unique; if even that collides in one space, the whole id is used.
func fileName(title string, id uuid.UUID, used map[string]bool) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "entry"
	}
	name := slug + "-" + id.String()[:8] + ".md"
	if used[name] {
		name = slug + "-" + id.String() + ".md"
	}
	used[name] = true
	return name
}

// linkText makes a title safe inside the brackets of a Markdown link.
func linkText(title string) string {
	r := strings.NewReplacer("[", "(", "]", ")", "\r", " ", "\n", " ")
	return r.Replace(title)
}

func spaceIndex(sp spaces.Space, lines []string) string {
	var b strings.Builder
	name := sp.Name
	if sp.Type == "personal" {
		name = "Personal space"
	}
	fmt.Fprintf(&b, "# %s\n\n", linkText(name))
	if len(lines) == 0 {
		b.WriteString("No entries.\n")
		return b.String()
	}
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	return b.String()
}

func rootIndex(res Result, now time.Time) string {
	var b strings.Builder
	// The root index is the only index with front matter: it names the version of the format.
	b.WriteString("---\nokf_version: \"0.2\"\n---\n\n")
	fmt.Fprintf(&b, "# Knowledge export of %s\n\n", now.UTC().Format("2006-01-02"))
	for _, sc := range res.Spaces {
		name, desc := sc.Space.Name, strings.Join(strings.Fields(sc.Space.Description), " ")
		if sc.Space.Type == "personal" {
			name, desc = "Personal space", ""
		}
		count := fmt.Sprintf("%d entries", sc.Entries)
		if desc != "" {
			desc += " (" + count + ")"
		} else {
			desc = count
		}
		fmt.Fprintf(&b, "* [%s](%s/) - %s\n", linkText(name), sc.Space.Slug, desc)
	}
	return b.String()
}
