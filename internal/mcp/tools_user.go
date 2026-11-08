package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/edition"
	"github.com/behringer24/atrium-tablinum/internal/product"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

func (s *Server) registerUserTools(ms *mcp.Server) {
	addTool(s, ms, "list_spaces", "Show spaces", tierUser, false, s.listSpaces)
	addTool(s, ms, "space_contacts", "Show curators and admins", tierUser, false, s.spaceContacts)
	s.registerSessionTools(ms)
	addTool(s, ms, "server_info", "Show version", tierUser, false, s.serverInfo)
	addTool(s, ms, "store_knowledge", "Store knowledge", tierUser, true, s.storeKnowledge)
	addTool(s, ms, "search_knowledge", "Search knowledge", tierUser, false, s.searchKnowledge)
	addTool(s, ms, "get_knowledge", "Load entry", tierUser, false, s.getKnowledge)
	addTool(s, ms, "update_knowledge", "Update entry", tierUser, true, s.updateKnowledge)
	addTool(s, ms, "archive_knowledge", "Archive entry", tierUser, true, s.archiveKnowledge)
	addTool(s, ms, "unarchive_knowledge", "Restore entry from archive", tierUser, true, s.unarchiveKnowledge)
	addTool(s, ms, "move_knowledge", "Move entry", tierUser, true, s.moveKnowledge)
	addTool(s, ms, "delete_knowledge", "Delete entry permanently", tierUser, true, s.deleteKnowledge)
	addTool(s, ms, "list_knowledge", "List entries", tierUser, false, s.listKnowledge)
	addTool(s, ms, "whats_new", "What is new", tierUser, false, s.whatsNew)
	addTool(s, ms, "set_default_space", "Set default space", tierUser, true, s.setDefaultSpace)
	addTool(s, ms, "handoff_save", "Save handoff", tierUser, true, s.handoffSave)
	addTool(s, ms, "handoff_list", "List handoffs", tierUser, false, s.handoffList)
	addTool(s, ms, "handoff_load", "Load handoff", tierUser, false, s.handoffLoad)
	s.registerSkillTools(ms)
	s.registerEditionTools(ms, tierUser)
}

// ---- shared output shapes ---------------------------------------------------

type spaceInfo struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	Role             string `json:"role"`
	Description      string `json:"description,omitempty"`
	RequiresApproval bool   `json:"requires_approval"`
	IsDefault        bool   `json:"is_default,omitempty"`
}

type docInfo struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Space      string   `json:"space"`
	SpaceType  string   `json:"space_type,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Content    string   `json:"content,omitempty"`
	Tags       []string `json:"tags"`
	Status     string   `json:"status"`
	ReviewNote string   `json:"review_note,omitempty"`
	Notice     string   `json:"notice,omitempty"`
	Author     string   `json:"author"`
	Source     string   `json:"source,omitempty"`
	Version    int      `json:"version,omitempty"`
	Updated    string   `json:"updated"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
	// Type is the knowledge type: rule is binding, documentation describes, recommendation and
	// experience are not binding.
	Type string `json:"type,omitempty"`
	// Owner is the responsible person if one was named; OwnerFormer marks somebody who has left.
	Owner       string `json:"owner,omitempty"`
	OwnerFormer bool   `json:"owner_former,omitempty"`
	// ReviewDue is the review date; ReviewOverdue is set once it has passed.
	ReviewDue     string `json:"review_due,omitempty"`
	ReviewOverdue bool   `json:"review_overdue,omitempty"`
}

func (s *Server) spaceInfos(list []spaces.Space, a docs.Actor) []spaceInfo {
	out := make([]spaceInfo, 0, len(list))
	for _, sp := range list {
		si := spaceInfo{Slug: sp.Slug, Name: sp.Name, Type: sp.Type, Role: a.Roles.Role(sp.ID).String(),
			Description: sp.Description, RequiresApproval: sp.RequireApproval}
		if sp.Type == "personal" {
			si.Name = "My personal space"
		}
		si.IsDefault = a.User.DefaultSpaceID != nil && *a.User.DefaultSpaceID == sp.ID
		out = append(out, si)
	}
	return out
}

func (s *Server) docInfo(ctx context.Context, d docs.Document, withContent bool) (docInfo, error) {
	sp, err := s.Spaces.GetByID(ctx, d.SpaceID)
	if err != nil {
		return docInfo{}, err
	}
	name := sp.Name
	if sp.Type == "personal" {
		name = "My personal space"
	}
	di := docInfo{ID: d.ID.String(), Title: d.Title, Space: name, SpaceType: sp.Type, Kind: d.Kind, Tags: d.Tags,
		Status: d.Status, ReviewNote: d.ReviewNote, Author: d.AuthorEmail, Source: d.Source, Version: d.Version,
		Updated: d.UpdatedAt.Format(time.RFC3339)}
	if d.Tags == nil {
		di.Tags = []string{}
	}
	if withContent {
		di.Content = d.Content
	}
	if d.ExpiresAt != nil {
		di.ExpiresAt = d.ExpiresAt.Format(time.RFC3339)
	}
	if d.Kind == "knowledge" {
		di.Type = d.Type
	}
	if d.OwnerID != nil {
		di.Owner, di.OwnerFormer = d.OwnerEmail, !d.OwnerActive
	}
	if d.ReviewDue != nil {
		now := time.Now().UTC()
		di.ReviewDue = d.ReviewDue.Format("2006-01-02")
		di.ReviewOverdue = d.ReviewDue.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
	}
	return di, nil
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, docs.ErrNotFound
	}
	return id, nil
}

func spaceNames(list []spaces.Space) string {
	names := make([]string, len(list))
	for i, sp := range list {
		names[i] = fmt.Sprintf("%s (%s)", sp.Name, sp.Slug)
	}
	return strings.Join(names, ", ")
}

// ---- list_spaces --------------------------------------------------------------

type listSpacesIn struct{}
type listSpacesOut struct {
	Spaces []spaceInfo `json:"spaces"`
}

func (s *Server) listSpaces(ctx context.Context, c *call, _ listSpacesIn) (listSpacesOut, error) {
	list, err := s.spacesAtLeast(ctx, c.Actor, authz.Reader)
	if err != nil {
		return listSpacesOut{}, err
	}
	return listSpacesOut{Spaces: s.spaceInfos(list, c.Actor)}, nil
}

// ---- space_contacts / server_info ----------------------------------------------------------

type contactsIn struct {
	Space string `json:"space,omitempty" jsonschema:"Optional: only this space (slug or name); empty = all team spaces of the user"`
}

type contactInfo struct {
	Type string `json:"type"` // user | group
	Name string `json:"name"` // e-mail or group name
	Role string `json:"role"` // curator | admin
	// Former marks a person who no longer has access (locked account): named for the record, not
	// someone to ask.
	Former bool `json:"former,omitempty"`
}

type spaceContacts struct {
	Space    string        `json:"space"`
	Slug     string        `json:"slug"`
	Contacts []contactInfo `json:"contacts"`
}

type contactsOut struct {
	Spaces []spaceContacts `json:"spaces"`
	// InstanceAdmins can act in every team space and are the last resort when a space has no admin.
	InstanceAdmins []string `json:"instance_admins"`
}

func (s *Server) spaceContacts(ctx context.Context, c *call, in contactsIn) (contactsOut, error) {
	visible, err := s.teamSpacesAtLeast(ctx, c.Actor, authz.Reader)
	if err != nil {
		return contactsOut{}, err
	}
	if strings.TrimSpace(in.Space) != "" {
		sp, cands := matchSpace(visible, in.Space)
		switch {
		case sp != nil:
			visible = []spaces.Space{*sp}
		case len(cands) > 1:
			return contactsOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", in.Space, spaceNames(cands)))
		default:
			return contactsOut{}, userErr(fmt.Sprintf("Space %q not found.", in.Space))
		}
	}
	out := contactsOut{Spaces: make([]spaceContacts, 0, len(visible)), InstanceAdmins: []string{}}
	for _, sp := range visible {
		ms, err := s.Spaces.Members(ctx, sp.ID)
		if err != nil {
			return contactsOut{}, err
		}
		sc := spaceContacts{Space: sp.Name, Slug: sp.Slug, Contacts: []contactInfo{}}
		// Only curators and admins: whoever reads here may ask whom to turn to, the list of
		// all members stays with the space admins (space_members).
		for _, m := range ms {
			if m.Role >= authz.Curator {
				sc.Contacts = append(sc.Contacts, contactInfo{Type: m.PrincipalType, Name: m.Name, Role: m.Role.String(), Former: m.Former})
			}
		}
		out.Spaces = append(out.Spaces, sc)
	}
	if len(out.Spaces) > 0 {
		if out.InstanceAdmins, err = s.Users.InstanceAdminEmails(ctx); err != nil {
			return contactsOut{}, err
		}
	}
	return out, nil
}

type serverInfoIn struct{}
type serverInfoOut struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	// Line is the release line of the build and its date; BuildDate the commit date, for support.
	Line      string `json:"line,omitempty"`
	LineDate  string `json:"line_date,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	// Edition in effect.
	Edition string `json:"edition,omitempty"`
}

func (s *Server) serverInfo(_ context.Context, _ *call, _ serverInfoIn) (serverInfoOut, error) {
	out := serverInfoOut{Name: s.set.ProductName, Version: product.Version, Commit: product.Commit(), BuildDate: product.BuildDate}
	line := product.CurrentLine()
	out.Line, out.LineDate = line.Name, line.Coverage().Format("2006-01-02")
	out.Edition = edition.Community
	if s.Edition != nil {
		out.Edition = s.Edition.Name()
	}
	return out, nil
}

// ---- store_knowledge ----------------------------------------------------------

type storeIn struct {
	Title   string   `json:"title" jsonschema:"Meaningful title of the entry"`
	Content string   `json:"content" jsonschema:"Self-contained content as Markdown"`
	Space   string   `json:"space,omitempty" jsonschema:"Target space (slug or name); empty = the user's default space"`
	Tags    []string `json:"tags,omitempty" jsonschema:"Optional keywords"`
	Force   bool     `json:"force,omitempty" jsonschema:"true = store despite similar entries (only after checking with the user)"`
	Draft   bool     `json:"draft,omitempty" jsonschema:"true = store as a draft that a curator of the space reviews and approves first (team spaces only, only on the explicit request of the user)"`
	Type    string   `json:"type,omitempty" jsonschema:"Optional knowledge type: rule (binding; curators and admins of the space only), documentation (default), recommendation or experience"`
	Owner   string   `json:"owner,omitempty" jsonschema:"Optional: e-mail of the person responsible for the entry (team spaces; they must be able to read the space). Only when the user names one"`
	// ReviewDue is a date so that an assistant cannot pass a relative phrase.
	ReviewDue string `json:"review_due,omitempty" jsonschema:"Optional: date (YYYY-MM-DD) by which the entry should be reviewed"`
}

type similarInfo struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Similarity float64 `json:"similarity"`
}

type storeOut struct {
	// Result is "stored", "similar_found" or "choose_space"; only "stored" saved anything.
	Result  string        `json:"result"`
	Message string        `json:"message"`
	ID      string        `json:"id,omitempty"`
	Space   string        `json:"space,omitempty"`
	Status  string        `json:"status,omitempty"`
	Similar []similarInfo `json:"similar,omitempty"`
	Spaces  []spaceInfo   `json:"spaces,omitempty"`
}

func (s *Server) storeKnowledge(ctx context.Context, c *call, in storeIn) (storeOut, error) {
	a := c.Actor
	writable, err := s.spacesAtLeast(ctx, a, authz.Author)
	if err != nil {
		return storeOut{}, err
	}

	var target *spaces.Space
	if strings.TrimSpace(in.Space) != "" {
		var cands []spaces.Space
		target, cands = matchSpace(writable, in.Space)
		if target == nil {
			if len(cands) == 0 {
				cands = writable
			}
			return storeOut{
				Result:  "choose_space",
				Message: fmt.Sprintf("The space %q is ambiguous or not writable. Nothing was saved - please ask the user which space is meant.", in.Space),
				Spaces:  s.spaceInfos(cands, a),
			}, nil
		}
	} else if target = s.defaultTarget(ctx, a, writable); target == nil {
		return storeOut{
			Result:  "choose_space",
			Message: "There is no default space. Nothing was saved - please ask the user which space to save to.",
			Spaces:  s.spaceInfos(writable, a),
		}, nil
	}

	p := docs.StoreParams{SpaceID: target.ID, Title: in.Title, Content: in.Content, Tags: in.Tags, Force: in.Force, Draft: in.Draft}
	if p.Type, err = parseType(in.Type); err != nil {
		return storeOut{}, err
	}
	if strings.TrimSpace(in.Owner) != "" {
		owner, err := s.readerUser(ctx, in.Owner, target.ID)
		if err != nil {
			return storeOut{}, err
		}
		p.OwnerID = &owner
	}
	if strings.TrimSpace(in.ReviewDue) != "" {
		if p.ReviewDue, err = parseReviewDue(in.ReviewDue); err != nil {
			return storeOut{}, err
		}
	}
	res, err := s.Docs.Store(ctx, a, p)
	if err != nil {
		return storeOut{}, err
	}
	if res.Document == nil {
		out := storeOut{Result: "similar_found",
			Message: "There are very similar entries in this space. Nothing was saved - ask the user whether an existing entry should be updated (update_knowledge) or a new one saved anyway (force=true)."}
		for _, d := range res.Duplicates {
			out.Similar = append(out.Similar, similarInfo{ID: d.ID.String(), Title: d.Title, Similarity: d.Similarity})
		}
		return out, nil
	}
	name := target.Name
	if target.Type == "personal" {
		name = "My personal space"
	}
	msg := fmt.Sprintf("Saved in the space \"%s\".", name)
	if res.Document.Status == "draft" {
		msg += " The entry is a draft and is visible to others only after approval by a curator of the space (review_knowledge)."
	}
	return storeOut{Result: "stored", Message: msg, ID: res.Document.ID.String(), Space: name, Status: res.Document.Status}, nil
}

// defaultTarget applies concept 7.7: the user's default space, then the group default
// from the config, then the personal space.
func (s *Server) defaultTarget(ctx context.Context, a docs.Actor, writable []spaces.Space) *spaces.Space {
	byID := func(id uuid.UUID) *spaces.Space {
		for i := range writable {
			if writable[i].ID == id {
				return &writable[i]
			}
		}
		return nil
	}
	if a.User.DefaultSpaceID != nil {
		if sp := byID(*a.User.DefaultSpaceID); sp != nil {
			return sp
		}
	}
	if len(s.set.DefaultSpaceByGroup) > 0 {
		groups, _ := s.Users.GroupNamesOf(ctx, a.User.ID)
		for _, g := range groups {
			if slug, ok := s.set.DefaultSpaceByGroup[g]; ok {
				for i := range writable {
					if writable[i].Slug == slug {
						return &writable[i]
					}
				}
			}
		}
	}
	if a.User.PersonalSpaceID != nil {
		return byID(*a.User.PersonalSpaceID)
	}
	return nil
}

// ---- search_knowledge -----------------------------------------------------------

type searchIn struct {
	Query   string   `json:"query" jsonschema:"Search query in natural language"`
	Spaces  []string `json:"spaces,omitempty" jsonschema:"Optional: search only in these spaces (slug or name)"`
	Tags    []string `json:"tags,omitempty" jsonschema:"Optional: only entries with one of these tags"`
	Limit   int      `json:"limit,omitempty" jsonschema:"Number of hits, default 5, at most 20"`
	Archive bool     `json:"archive,omitempty" jsonschema:"true = search ONLY the archive (instead of the active entries), only on the explicit request of the user"`
	Types   []string `json:"types,omitempty" jsonschema:"Optional: only entries of these knowledge types (rule, documentation, recommendation, experience)"`
}

type hitInfo struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Type      string  `json:"type"`
	Space     string  `json:"space"`
	SpaceType string  `json:"space_type"`
	Section   string  `json:"section,omitempty"`
	Excerpt   string  `json:"excerpt"`
	Status    string  `json:"status"`
	Author    string  `json:"author"`
	Updated   string  `json:"updated"`
	Score     float64 `json:"score"`
}

type searchOut struct {
	Notice  string    `json:"notice,omitempty"`
	Message string    `json:"message,omitempty"`
	Results []hitInfo `json:"results"`
}

func (s *Server) searchKnowledge(ctx context.Context, c *call, in searchIn) (searchOut, error) {
	a := c.Actor
	p := search.Params{Query: in.Query, Tags: in.Tags, Limit: in.Limit, Archive: in.Archive}
	for _, t := range in.Types {
		typ, err := parseType(t)
		if err != nil {
			return searchOut{}, err
		}
		if typ != "" {
			p.Types = append(p.Types, typ)
		}
	}
	if len(in.Spaces) > 0 {
		visible, err := s.spacesAtLeast(ctx, a, authz.Reader)
		if err != nil {
			return searchOut{}, err
		}
		for _, ref := range in.Spaces {
			sp, cands := matchSpace(visible, ref)
			switch {
			case sp != nil:
				p.Spaces = append(p.Spaces, sp.ID)
			case len(cands) > 1:
				return searchOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", ref, spaceNames(cands)))
			default:
				return searchOut{}, userErr(fmt.Sprintf("Space %q not found.", ref))
			}
		}
	}
	rs, err := s.Search.Search(ctx, search.Caller{Scope: a.Scope(), PersonalSpace: a.User.PersonalSpaceID, Client: a.Client}, p)
	if err != nil {
		return searchOut{}, err
	}
	out := searchOut{Results: make([]hitInfo, 0, len(rs)), Notice: untrustedNotice}
	for _, r := range rs {
		name := r.SpaceName
		if r.SpaceType == "personal" {
			name = "My personal space"
		}
		out.Results = append(out.Results, hitInfo{
			ID: r.DocumentID.String(), Title: r.Title, Type: r.Type, Space: name, SpaceType: r.SpaceType, Section: r.HeadingPath,
			Excerpt: r.Content, Status: r.Status, Author: r.AuthorEmail, Updated: r.UpdatedAt.Format(time.RFC3339), Score: r.Score,
		})
	}
	switch {
	case len(out.Results) == 0 && in.Archive:
		out.Message = "No matching entries found in the archive."
	case len(out.Results) == 0:
		out.Message = "No matching entries found."
	case in.Archive:
		out.Message = "Hits from the archive: these entries are archived and not currently valid. They can be brought back with unarchive_knowledge."
	}
	return out, nil
}

// ---- get / update / archive / list ----------------------------------------------

type idIn struct {
	ID string `json:"id" jsonschema:"ID of the entry"`
}

func (s *Server) getKnowledge(ctx context.Context, c *call, in idIn) (docInfo, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return docInfo{}, err
	}
	d, err := s.Docs.Get(ctx, c.Actor, id)
	if err != nil {
		return docInfo{}, err
	}
	di, err := s.docInfo(ctx, d, true)
	di.Notice = untrustedNotice
	return di, err
}

type updateIn struct {
	ID      string    `json:"id" jsonschema:"ID of the entry"`
	Title   *string   `json:"title,omitempty" jsonschema:"New title"`
	Content *string   `json:"content,omitempty" jsonschema:"Complete new content (Markdown)"`
	Tags    *[]string `json:"tags,omitempty" jsonschema:"New complete tag list"`
	Type    *string   `json:"type,omitempty" jsonschema:"New knowledge type: rule (binding; curators and admins of the space only), documentation, recommendation or experience"`
	Owner   *string   `json:"owner,omitempty" jsonschema:"E-mail of the new responsible person (team spaces; must be able to read the space); an empty string removes the responsible person"`
	// ReviewDue is a date so that an assistant cannot pass a relative phrase.
	ReviewDue *string `json:"review_due,omitempty" jsonschema:"New review date (YYYY-MM-DD); an empty string removes it"`
}

func (s *Server) updateKnowledge(ctx context.Context, c *call, in updateIn) (docInfo, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return docInfo{}, err
	}
	if in.Title == nil && in.Content == nil && in.Tags == nil && in.Type == nil && in.Owner == nil && in.ReviewDue == nil {
		return docInfo{}, userErr("Nothing to change was passed.")
	}
	p := docs.UpdateParams{ID: id, Title: in.Title, Content: in.Content, Tags: in.Tags}
	if in.Type != nil {
		t, err := parseType(*in.Type)
		if err != nil {
			return docInfo{}, err
		}
		if t == "" {
			t = docs.TypeDocumentation
		}
		p.Type = &t
	}
	if in.Owner != nil {
		if strings.TrimSpace(*in.Owner) == "" {
			p.ClearOwner = true
		} else {
			cur, err := s.Docs.Get(ctx, c.Actor, id)
			if err != nil {
				return docInfo{}, err
			}
			owner, err := s.readerUser(ctx, *in.Owner, cur.SpaceID)
			if err != nil {
				return docInfo{}, err
			}
			p.OwnerID = &owner
		}
	}
	if in.ReviewDue != nil {
		if strings.TrimSpace(*in.ReviewDue) == "" {
			p.ClearReviewDue = true
		} else if p.ReviewDue, err = parseReviewDue(*in.ReviewDue); err != nil {
			return docInfo{}, err
		}
	}
	d, err := s.Docs.Update(ctx, c.Actor, p)
	if err != nil {
		return docInfo{}, err
	}
	return s.docInfo(ctx, d, false)
}

type archiveIn struct {
	ID      string `json:"id" jsonschema:"ID of the entry"`
	Confirm bool   `json:"confirm" jsonschema:"true only after explicit confirmation by the user"`
}
type messageOut struct {
	Message string `json:"message"`
}

func (s *Server) archiveKnowledge(ctx context.Context, c *call, in archiveIn) (messageOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return messageOut{}, err
	}
	if !in.Confirm {
		return messageOut{}, userErr("Archiving needs a confirmation: ask the user and then call again with confirm=true.")
	}
	if err := s.Docs.Archive(ctx, c.Actor, id); err != nil {
		return messageOut{}, err
	}
	return messageOut{Message: "The entry has been archived."}, nil
}

func (s *Server) unarchiveKnowledge(ctx context.Context, c *call, in archiveIn) (unarchiveOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return unarchiveOut{}, err
	}
	if !in.Confirm {
		return unarchiveOut{}, userErr("Restoring makes the entry readable and searchable again for all readers of the space: ask the user and then call again with confirm=true.")
	}
	d, err := s.Docs.Unarchive(ctx, c.Actor, id)
	if err != nil {
		return unarchiveOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return unarchiveOut{}, err
	}
	msg := "The entry is active again."
	if d.Status == "draft" {
		msg = "The entry has been restored, but in a space that requires approval it counts as a draft and is visible to others only after approval by a curator."
	}
	return unarchiveOut{Message: msg, Document: info}, nil
}

type unarchiveOut struct {
	Message  string  `json:"message"`
	Document docInfo `json:"document"`
}

type moveIn struct {
	ID      string `json:"id" jsonschema:"ID of the entry"`
	Space   string `json:"space" jsonschema:"Target space (slug or name)"`
	Confirm bool   `json:"confirm" jsonschema:"true only after explicit confirmation by the user"`
}
type moveOut struct {
	Result   string      `json:"result"` // moved | choose_space
	Message  string      `json:"message"`
	Document *docInfo    `json:"document,omitempty"`
	Spaces   []spaceInfo `json:"spaces,omitempty"`
}

func (s *Server) moveKnowledge(ctx context.Context, c *call, in moveIn) (moveOut, error) {
	a := c.Actor
	id, err := parseID(in.ID)
	if err != nil {
		return moveOut{}, err
	}
	// Only spaces with write permission qualify as a target.
	writable, err := s.spacesAtLeast(ctx, a, authz.Author)
	if err != nil {
		return moveOut{}, err
	}
	target, cands := matchSpace(writable, in.Space)
	if target == nil {
		if len(cands) == 0 {
			cands = writable
		}
		return moveOut{
			Result:  "choose_space",
			Message: fmt.Sprintf("The space %q is ambiguous or you lack write permission there. Nothing was moved - please ask the user which space is meant.", in.Space),
			Spaces:  s.spaceInfos(cands, a),
		}, nil
	}
	cur, err := s.Docs.Get(ctx, a, id)
	if err != nil {
		return moveOut{}, err
	}
	name := target.Name
	if target.Type == "personal" {
		name = "My personal space"
	}
	if !in.Confirm {
		return moveOut{}, userErr(fmt.Sprintf("Moving \"%s\" to \"%s\" needs a confirmation: ask the user (other members will then see the entry there, no longer in the old space) and then call again with confirm=true.", cur.Title, name))
	}
	d, err := s.Docs.Move(ctx, a, docs.MoveParams{ID: id, SpaceID: target.ID})
	if err != nil {
		return moveOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return moveOut{}, err
	}
	msg := fmt.Sprintf("The entry has been moved to \"%s\".", name)
	if d.Status == "draft" {
		msg += " Approval is required there: the entry is now a draft and is visible to others only after approval by a curator."
	}
	return moveOut{Result: "moved", Message: msg, Document: &info}, nil
}

func (s *Server) deleteKnowledge(ctx context.Context, c *call, in archiveIn) (messageOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return messageOut{}, err
	}
	if !in.Confirm {
		return messageOut{}, userErr("Permanent deletion needs a confirmation: tell the user that the entry will be irrevocably removed along with its versions (archive_knowledge is enough to hide it), ask them, and then call again with confirm=true.")
	}
	if err := s.Docs.Delete(ctx, c.Actor, id); err != nil {
		return messageOut{}, err
	}
	return messageOut{Message: "The entry has been permanently deleted."}, nil
}

type listIn struct {
	Space  string `json:"space,omitempty" jsonschema:"Space (slug or name); empty = personal space"`
	Tag    string `json:"tag,omitempty" jsonschema:"Optional: only entries with this tag"`
	Status string `json:"status,omitempty" jsonschema:"Optional: only draft (waiting for approval), approved, rejected (returned) or archived (archive)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Count, default 20, at most 100"`
	Type   string `json:"type,omitempty" jsonschema:"Optional: only entries of this knowledge type (rule, documentation, recommendation, experience)"`
	Owner  string `json:"owner,omitempty" jsonschema:"Optional: only entries this person (e-mail) is responsible for, also a former one"`
	Review string `json:"review,omitempty" jsonschema:"Optional: due = only entries whose review date has passed"`
}
type listOut struct {
	Notice  string    `json:"notice,omitempty"`
	Space   string    `json:"space"`
	Entries []docInfo `json:"entries"`
}

func (s *Server) listKnowledge(ctx context.Context, c *call, in listIn) (listOut, error) {
	a := c.Actor
	visible, err := s.spacesAtLeast(ctx, a, authz.Reader)
	if err != nil {
		return listOut{}, err
	}
	ref := in.Space
	if strings.TrimSpace(ref) == "" {
		ref = "personal"
	}
	sp, cands := matchSpace(visible, ref)
	switch {
	case sp == nil && len(cands) > 1:
		return listOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", ref, spaceNames(cands)))
	case sp == nil:
		return listOut{}, docs.ErrNotFound
	}
	lp := docs.ListParams{SpaceID: sp.ID, Tag: strings.ToLower(strings.TrimSpace(in.Tag)),
		Status: strings.ToLower(strings.TrimSpace(in.Status)), Limit: in.Limit}
	if lp.Type, err = parseType(in.Type); err != nil {
		return listOut{}, err
	}
	if strings.TrimSpace(in.Owner) != "" {
		u, err := s.knownUser(ctx, in.Owner)
		if err != nil {
			return listOut{}, err
		}
		lp.OwnerID = &u.ID
	}
	switch strings.ToLower(strings.TrimSpace(in.Review)) {
	case "":
	case "due":
		lp.ReviewDue = true
	default:
		return listOut{}, userErr("review must be due or empty.")
	}
	list, err := s.Docs.List(ctx, a, lp)
	if err != nil {
		return listOut{}, err
	}
	out := listOut{Space: sp.Name, Entries: make([]docInfo, 0, len(list)), Notice: untrustedNotice}
	if sp.Type == "personal" {
		out.Space = "My personal space"
	}
	for _, d := range list {
		di, err := s.docInfo(ctx, d, false)
		if err != nil {
			return listOut{}, err
		}
		out.Entries = append(out.Entries, di)
	}
	return out, nil
}

// ---- set_default_space ------------------------------------------------------------

type defaultSpaceIn struct {
	Space string `json:"space,omitempty" jsonschema:"Space (slug or name); empty = reset"`
}

func (s *Server) setDefaultSpace(ctx context.Context, c *call, in defaultSpaceIn) (messageOut, error) {
	a := c.Actor
	if strings.TrimSpace(in.Space) == "" {
		if err := s.Users.SetDefaultSpace(ctx, a.User.ID, nil); err != nil {
			return messageOut{}, err
		}
		return messageOut{Message: "Default space reset, knowledge is saved to the personal space again."}, nil
	}
	writable, err := s.spacesAtLeast(ctx, a, authz.Author)
	if err != nil {
		return messageOut{}, err
	}
	sp, cands := matchSpace(writable, in.Space)
	if sp == nil {
		if len(cands) > 1 {
			return messageOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", in.Space, spaceNames(cands)))
		}
		return messageOut{}, userErr(fmt.Sprintf("Space %q not found or not writable.", in.Space))
	}
	if err := s.Users.SetDefaultSpace(ctx, a.User.ID, &sp.ID); err != nil {
		return messageOut{}, err
	}
	name := sp.Name
	if sp.Type == "personal" {
		name = "My personal space"
	}
	return messageOut{Message: fmt.Sprintf("The default space is now \"%s\".", name)}, nil
}

// ---- handoffs ----------------------------------------------------------------------

type handoffSaveIn struct {
	Title   string `json:"title" jsonschema:"Short title of the handoff"`
	Content string `json:"content" jsonschema:"Goal, results so far, open points, next steps"`
	TTLDays int    `json:"ttl_days,omitempty" jsonschema:"Retention in days (instance default, at most 90)"`
}
type handoffSaveOut struct {
	Message string `json:"message"`
	ID      string `json:"id"`
	Expires string `json:"expires_at"`
}

func (s *Server) handoffSave(ctx context.Context, c *call, in handoffSaveIn) (handoffSaveOut, error) {
	d, err := s.Docs.SaveHandoff(ctx, c.Actor, in.Title, in.Content, in.TTLDays)
	if err != nil {
		return handoffSaveOut{}, err
	}
	exp := ""
	if d.ExpiresAt != nil {
		exp = d.ExpiresAt.Format(time.RFC3339)
	}
	return handoffSaveOut{Message: "Handoff stored in the personal space. It can be loaded in another assistant with handoff_load.", ID: d.ID.String(), Expires: exp}, nil
}

type handoffListIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"Count, default 20, at most 50"`
}
type handoffListOut struct {
	Handoffs []docInfo `json:"handoffs"`
}

func (s *Server) handoffList(ctx context.Context, c *call, in handoffListIn) (handoffListOut, error) {
	list, err := s.Docs.ListHandoffs(ctx, c.Actor, in.Limit)
	if err != nil {
		return handoffListOut{}, err
	}
	out := handoffListOut{Handoffs: make([]docInfo, 0, len(list))}
	for _, d := range list {
		di, err := s.docInfo(ctx, d, false)
		if err != nil {
			return handoffListOut{}, err
		}
		out.Handoffs = append(out.Handoffs, di)
	}
	return out, nil
}

type handoffLoadIn struct {
	ID string `json:"id,omitempty" jsonschema:"ID of the handoff; empty = newest"`
}

func (s *Server) handoffLoad(ctx context.Context, c *call, in handoffLoadIn) (docInfo, error) {
	var id *uuid.UUID
	if strings.TrimSpace(in.ID) != "" {
		parsed, err := parseID(in.ID)
		if err != nil {
			return docInfo{}, err
		}
		id = &parsed
	}
	d, err := s.Docs.LoadHandoff(ctx, c.Actor, id)
	if err != nil {
		return docInfo{}, err
	}
	di, err := s.docInfo(ctx, d, true)
	di.Notice = untrustedNotice
	return di, err
}
