package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// registerCuratorTools adds what only curators (and admins) of a team space need. Like all
// tiers it is a convenience for the tool list; docs.Review re-checks the role per entry.
func (s *Server) registerCuratorTools(ms *mcp.Server) {
	addTool(s, ms, "review_knowledge", "Entwurf prüfen", tierCurator, true, s.reviewKnowledge)
}

type reviewIn struct {
	ID       string `json:"id" jsonschema:"ID des Entwurfs"`
	Decision string `json:"decision" jsonschema:"approve = freigeben, reject = mit Anmerkung an den Autor zurückgeben"`
	Note     string `json:"note,omitempty" jsonschema:"Anmerkung für den Autor; beim Zurückgeben Pflicht"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"Bei approve: true erst nach ausdrücklicher Bestätigung durch den Nutzer"`
}

type reviewOut struct {
	Message  string  `json:"message"`
	Document docInfo `json:"document"`
}

func (s *Server) reviewKnowledge(ctx context.Context, c *call, in reviewIn) (reviewOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return reviewOut{}, err
	}
	var approve bool
	switch strings.ToLower(strings.TrimSpace(in.Decision)) {
	case "approve":
		approve = true
	case "reject":
	default:
		return reviewOut{}, userErr("decision muss approve oder reject sein.")
	}
	if approve && !in.Confirm {
		return reviewOut{}, userErr("Die Freigabe macht den Eintrag für alle Leser des Bereichs sichtbar: den Entwurf dem Nutzer vorlegen, nach seiner Bestätigung fragen und danach mit confirm=true erneut aufrufen.")
	}
	d, err := s.Docs.Review(ctx, c.Actor, docs.ReviewParams{ID: id, Approve: approve, Note: in.Note})
	if err != nil {
		return reviewOut{}, err
	}
	info, err := s.docInfo(ctx, d, false)
	if err != nil {
		return reviewOut{}, err
	}
	msg := "Der Entwurf wurde freigegeben und ist jetzt für alle Leser des Bereichs sichtbar."
	if !approve {
		msg = "Der Entwurf wurde mit deiner Anmerkung zurückgegeben. Der Autor sieht sie am Eintrag und kann ihn überarbeiten, danach geht er erneut in die Prüfung."
	}
	return reviewOut{Message: msg, Document: info}, nil
}
