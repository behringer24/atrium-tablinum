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
	"github.com/behringer24/atrium-tablinum/internal/product"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

func (s *Server) registerUserTools(ms *mcp.Server) {
	addTool(s, ms, "list_spaces", "Bereiche anzeigen", tierUser, false, s.listSpaces)
	addTool(s, ms, "space_contacts", "Kuratoren und Admins anzeigen", tierUser, false, s.spaceContacts)
	s.registerSessionTools(ms)
	addTool(s, ms, "server_info", "Version anzeigen", tierUser, false, s.serverInfo)
	addTool(s, ms, "store_knowledge", "Wissen speichern", tierUser, true, s.storeKnowledge)
	addTool(s, ms, "search_knowledge", "Wissen durchsuchen", tierUser, false, s.searchKnowledge)
	addTool(s, ms, "get_knowledge", "Eintrag laden", tierUser, false, s.getKnowledge)
	addTool(s, ms, "update_knowledge", "Eintrag ändern", tierUser, true, s.updateKnowledge)
	addTool(s, ms, "archive_knowledge", "Eintrag archivieren", tierUser, true, s.archiveKnowledge)
	addTool(s, ms, "unarchive_knowledge", "Eintrag aus dem Archiv holen", tierUser, true, s.unarchiveKnowledge)
	addTool(s, ms, "move_knowledge", "Eintrag verschieben", tierUser, true, s.moveKnowledge)
	addTool(s, ms, "delete_knowledge", "Eintrag endgültig löschen", tierUser, true, s.deleteKnowledge)
	addTool(s, ms, "list_knowledge", "Einträge auflisten", tierUser, false, s.listKnowledge)
	addTool(s, ms, "set_default_space", "Standardbereich setzen", tierUser, true, s.setDefaultSpace)
	addTool(s, ms, "handoff_save", "Übergabe ablegen", tierUser, true, s.handoffSave)
	addTool(s, ms, "handoff_list", "Übergaben auflisten", tierUser, false, s.handoffList)
	addTool(s, ms, "handoff_load", "Übergabe laden", tierUser, false, s.handoffLoad)
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
}

func (s *Server) spaceInfos(list []spaces.Space, a docs.Actor) []spaceInfo {
	out := make([]spaceInfo, 0, len(list))
	for _, sp := range list {
		si := spaceInfo{Slug: sp.Slug, Name: sp.Name, Type: sp.Type, Role: a.Roles.Role(sp.ID).String(),
			Description: sp.Description, RequiresApproval: sp.RequireApproval}
		if sp.Type == "personal" {
			si.Name = "Mein persönlicher Bereich"
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
		name = "Mein persönlicher Bereich"
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
	Space string `json:"space,omitempty" jsonschema:"Optional: nur dieser Bereich (Kürzel oder Name); leer = alle Team-Bereiche des Nutzers"`
}

type contactInfo struct {
	Type string `json:"type"` // user | group
	Name string `json:"name"` // e-mail or group name
	Role string `json:"role"` // curator | admin
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
			return contactsOut{}, userErr(fmt.Sprintf("Der Bereich %q ist mehrdeutig: %s.", in.Space, spaceNames(cands)))
		default:
			return contactsOut{}, userErr(fmt.Sprintf("Bereich %q nicht gefunden.", in.Space))
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
				sc.Contacts = append(sc.Contacts, contactInfo{Type: m.PrincipalType, Name: m.Name, Role: m.Role.String()})
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
}

func (s *Server) serverInfo(_ context.Context, _ *call, _ serverInfoIn) (serverInfoOut, error) {
	return serverInfoOut{Name: s.set.ProductName, Version: product.Version, Commit: product.Commit()}, nil
}

// ---- store_knowledge ----------------------------------------------------------

type storeIn struct {
	Title   string   `json:"title" jsonschema:"Aussagekräftiger Titel des Eintrags"`
	Content string   `json:"content" jsonschema:"Eigenständig verständlicher Inhalt als Markdown"`
	Space   string   `json:"space,omitempty" jsonschema:"Zielbereich (Kürzel oder Name); leer = Standardbereich des Nutzers"`
	Tags    []string `json:"tags,omitempty" jsonschema:"Optionale Schlagwörter"`
	Force   bool     `json:"force,omitempty" jsonschema:"true = trotz ähnlicher Einträge speichern (nur nach Rückfrage beim Nutzer)"`
	Draft   bool     `json:"draft,omitempty" jsonschema:"true = als Entwurf speichern, den erst ein Kurator des Bereichs prüft und freigibt (nur Team-Bereiche, nur auf ausdrücklichen Wunsch des Nutzers)"`
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
				Message: fmt.Sprintf("Der Bereich %q ist nicht eindeutig oder nicht beschreibbar. Nichts wurde gespeichert – bitte den Nutzer fragen, welcher Bereich gemeint ist.", in.Space),
				Spaces:  s.spaceInfos(cands, a),
			}, nil
		}
	} else if target = s.defaultTarget(ctx, a, writable); target == nil {
		return storeOut{
			Result:  "choose_space",
			Message: "Es gibt keinen Standardbereich. Nichts wurde gespeichert – bitte den Nutzer fragen, in welchem Bereich gespeichert werden soll.",
			Spaces:  s.spaceInfos(writable, a),
		}, nil
	}

	res, err := s.Docs.Store(ctx, a, docs.StoreParams{SpaceID: target.ID, Title: in.Title, Content: in.Content, Tags: in.Tags, Force: in.Force, Draft: in.Draft})
	if err != nil {
		return storeOut{}, err
	}
	if res.Document == nil {
		out := storeOut{Result: "similar_found",
			Message: "Es gibt sehr ähnliche Einträge in diesem Bereich. Nichts wurde gespeichert – den Nutzer fragen, ob ein bestehender Eintrag aktualisiert (update_knowledge) oder trotzdem neu gespeichert werden soll (force=true)."}
		for _, d := range res.Duplicates {
			out.Similar = append(out.Similar, similarInfo{ID: d.ID.String(), Title: d.Title, Similarity: d.Similarity})
		}
		return out, nil
	}
	name := target.Name
	if target.Type == "personal" {
		name = "Mein persönlicher Bereich"
	}
	msg := fmt.Sprintf("Gespeichert im Bereich „%s“.", name)
	if res.Document.Status == "draft" {
		msg += " Der Eintrag ist ein Entwurf und für andere erst nach Freigabe durch einen Kurator des Bereichs sichtbar (review_knowledge)."
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
	Query   string   `json:"query" jsonschema:"Suchanfrage in natürlicher Sprache"`
	Spaces  []string `json:"spaces,omitempty" jsonschema:"Optional: nur in diesen Bereichen suchen (Kürzel oder Name)"`
	Tags    []string `json:"tags,omitempty" jsonschema:"Optional: nur Einträge mit einem dieser Tags"`
	Limit   int      `json:"limit,omitempty" jsonschema:"Anzahl Treffer, Standard 5, höchstens 20"`
	Archive bool     `json:"archive,omitempty" jsonschema:"true = NUR im Archiv suchen (statt in den aktiven Einträgen), nur auf ausdrücklichen Wunsch des Nutzers"`
}

type hitInfo struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
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
				return searchOut{}, userErr(fmt.Sprintf("Der Bereich %q ist mehrdeutig: %s.", ref, spaceNames(cands)))
			default:
				return searchOut{}, userErr(fmt.Sprintf("Bereich %q nicht gefunden.", ref))
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
			name = "Mein persönlicher Bereich"
		}
		out.Results = append(out.Results, hitInfo{
			ID: r.DocumentID.String(), Title: r.Title, Space: name, SpaceType: r.SpaceType, Section: r.HeadingPath,
			Excerpt: r.Content, Status: r.Status, Author: r.AuthorEmail, Updated: r.UpdatedAt.Format(time.RFC3339), Score: r.Score,
		})
	}
	switch {
	case len(out.Results) == 0 && in.Archive:
		out.Message = "Keine passenden Einträge im Archiv gefunden."
	case len(out.Results) == 0:
		out.Message = "Keine passenden Einträge gefunden."
	case in.Archive:
		out.Message = "Treffer aus dem Archiv: Diese Einträge sind archiviert, nicht aktuell gültig. Mit unarchive_knowledge lassen sie sich zurückholen."
	}
	return out, nil
}

// ---- get / update / archive / list ----------------------------------------------

type idIn struct {
	ID string `json:"id" jsonschema:"ID des Eintrags"`
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
	ID      string    `json:"id" jsonschema:"ID des Eintrags"`
	Title   *string   `json:"title,omitempty" jsonschema:"Neuer Titel"`
	Content *string   `json:"content,omitempty" jsonschema:"Vollständiger neuer Inhalt (Markdown)"`
	Tags    *[]string `json:"tags,omitempty" jsonschema:"Neue vollständige Tag-Liste"`
}

func (s *Server) updateKnowledge(ctx context.Context, c *call, in updateIn) (docInfo, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return docInfo{}, err
	}
	if in.Title == nil && in.Content == nil && in.Tags == nil {
		return docInfo{}, userErr("Es wurde nichts zum Ändern übergeben.")
	}
	d, err := s.Docs.Update(ctx, c.Actor, docs.UpdateParams{ID: id, Title: in.Title, Content: in.Content, Tags: in.Tags})
	if err != nil {
		return docInfo{}, err
	}
	return s.docInfo(ctx, d, false)
}

type archiveIn struct {
	ID      string `json:"id" jsonschema:"ID des Eintrags"`
	Confirm bool   `json:"confirm" jsonschema:"true erst nach ausdrücklicher Bestätigung durch den Nutzer"`
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
		return messageOut{}, userErr("Archivieren braucht eine Bestätigung: den Nutzer fragen und danach mit confirm=true erneut aufrufen.")
	}
	if err := s.Docs.Archive(ctx, c.Actor, id); err != nil {
		return messageOut{}, err
	}
	return messageOut{Message: "Der Eintrag wurde archiviert."}, nil
}

func (s *Server) unarchiveKnowledge(ctx context.Context, c *call, in archiveIn) (unarchiveOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return unarchiveOut{}, err
	}
	if !in.Confirm {
		return unarchiveOut{}, userErr("Das Zurückholen macht den Eintrag wieder für alle Leser des Bereichs les- und suchbar: den Nutzer fragen und danach mit confirm=true erneut aufrufen.")
	}
	d, err := s.Docs.Unarchive(ctx, c.Actor, id)
	if err != nil {
		return unarchiveOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return unarchiveOut{}, err
	}
	msg := "Der Eintrag ist wieder aktiv."
	if d.Status == "draft" {
		msg = "Der Eintrag ist zurückgeholt, gilt im Bereich mit Freigabepflicht aber als Entwurf und ist für andere erst nach Freigabe durch einen Kurator sichtbar."
	}
	return unarchiveOut{Message: msg, Document: info}, nil
}

type unarchiveOut struct {
	Message  string  `json:"message"`
	Document docInfo `json:"document"`
}

type moveIn struct {
	ID      string `json:"id" jsonschema:"ID des Eintrags"`
	Space   string `json:"space" jsonschema:"Zielbereich (Kürzel oder Name)"`
	Confirm bool   `json:"confirm" jsonschema:"true erst nach ausdrücklicher Bestätigung durch den Nutzer"`
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
			Message: fmt.Sprintf("Der Bereich %q ist nicht eindeutig oder dort fehlt das Schreibrecht. Nichts wurde verschoben – bitte den Nutzer fragen, welcher Bereich gemeint ist.", in.Space),
			Spaces:  s.spaceInfos(cands, a),
		}, nil
	}
	cur, err := s.Docs.Get(ctx, a, id)
	if err != nil {
		return moveOut{}, err
	}
	name := target.Name
	if target.Type == "personal" {
		name = "Mein persönlicher Bereich"
	}
	if !in.Confirm {
		return moveOut{}, userErr(fmt.Sprintf("Das Verschieben von „%s“ nach „%s“ braucht eine Bestätigung: den Nutzer fragen (andere Mitglieder sehen den Eintrag danach dort, nicht mehr im alten Bereich) und danach mit confirm=true erneut aufrufen.", cur.Title, name))
	}
	d, err := s.Docs.Move(ctx, a, docs.MoveParams{ID: id, SpaceID: target.ID})
	if err != nil {
		return moveOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return moveOut{}, err
	}
	msg := fmt.Sprintf("Der Eintrag wurde nach „%s“ verschoben.", name)
	if d.Status == "draft" {
		msg += " Dort gilt eine Freigabepflicht: Der Eintrag ist jetzt ein Entwurf und für andere erst nach Freigabe durch einen Kurator sichtbar."
	}
	return moveOut{Result: "moved", Message: msg, Document: &info}, nil
}

func (s *Server) deleteKnowledge(ctx context.Context, c *call, in archiveIn) (messageOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return messageOut{}, err
	}
	if !in.Confirm {
		return messageOut{}, userErr("Endgültiges Löschen braucht eine Bestätigung: den Nutzer fragen, dass der Eintrag samt Versionen unwiderruflich entfernt wird (zum Ausblenden reicht archive_knowledge), und danach mit confirm=true erneut aufrufen.")
	}
	if err := s.Docs.Delete(ctx, c.Actor, id); err != nil {
		return messageOut{}, err
	}
	return messageOut{Message: "Der Eintrag wurde endgültig gelöscht."}, nil
}

type listIn struct {
	Space  string `json:"space,omitempty" jsonschema:"Bereich (Kürzel oder Name); leer = persönlicher Bereich"`
	Tag    string `json:"tag,omitempty" jsonschema:"Optional: nur Einträge mit diesem Tag"`
	Status string `json:"status,omitempty" jsonschema:"Optional: nur draft (wartet auf Freigabe), approved, rejected (zurückgegeben) oder archived (Archiv)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Anzahl, Standard 20, höchstens 100"`
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
		return listOut{}, userErr(fmt.Sprintf("Der Bereich %q ist mehrdeutig: %s.", ref, spaceNames(cands)))
	case sp == nil:
		return listOut{}, docs.ErrNotFound
	}
	list, err := s.Docs.List(ctx, a, docs.ListParams{SpaceID: sp.ID, Tag: strings.ToLower(strings.TrimSpace(in.Tag)),
		Status: strings.ToLower(strings.TrimSpace(in.Status)), Limit: in.Limit})
	if err != nil {
		return listOut{}, err
	}
	out := listOut{Space: sp.Name, Entries: make([]docInfo, 0, len(list)), Notice: untrustedNotice}
	if sp.Type == "personal" {
		out.Space = "Mein persönlicher Bereich"
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
	Space string `json:"space,omitempty" jsonschema:"Bereich (Kürzel oder Name); leer = zurücksetzen"`
}

func (s *Server) setDefaultSpace(ctx context.Context, c *call, in defaultSpaceIn) (messageOut, error) {
	a := c.Actor
	if strings.TrimSpace(in.Space) == "" {
		if err := s.Users.SetDefaultSpace(ctx, a.User.ID, nil); err != nil {
			return messageOut{}, err
		}
		return messageOut{Message: "Standardbereich zurückgesetzt, gespeichert wird wieder im persönlichen Bereich."}, nil
	}
	writable, err := s.spacesAtLeast(ctx, a, authz.Author)
	if err != nil {
		return messageOut{}, err
	}
	sp, cands := matchSpace(writable, in.Space)
	if sp == nil {
		if len(cands) > 1 {
			return messageOut{}, userErr(fmt.Sprintf("Der Bereich %q ist mehrdeutig: %s.", in.Space, spaceNames(cands)))
		}
		return messageOut{}, userErr(fmt.Sprintf("Bereich %q nicht gefunden oder nicht beschreibbar.", in.Space))
	}
	if err := s.Users.SetDefaultSpace(ctx, a.User.ID, &sp.ID); err != nil {
		return messageOut{}, err
	}
	name := sp.Name
	if sp.Type == "personal" {
		name = "Mein persönlicher Bereich"
	}
	return messageOut{Message: fmt.Sprintf("Standardbereich ist jetzt „%s“.", name)}, nil
}

// ---- handoffs ----------------------------------------------------------------------

type handoffSaveIn struct {
	Title   string `json:"title" jsonschema:"Kurzer Titel der Übergabe"`
	Content string `json:"content" jsonschema:"Ziel, bisherige Ergebnisse, offene Punkte, nächste Schritte"`
	TTLDays int    `json:"ttl_days,omitempty" jsonschema:"Aufbewahrung in Tagen (Standard laut Instanz, höchstens 90)"`
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
	return handoffSaveOut{Message: "Übergabe im persönlichen Bereich abgelegt. Sie kann in einem anderen Assistenten mit handoff_load geladen werden.", ID: d.ID.String(), Expires: exp}, nil
}

type handoffListIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"Anzahl, Standard 20, höchstens 50"`
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
	ID string `json:"id,omitempty" jsonschema:"ID der Übergabe; leer = neueste"`
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
