package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
)

// Knowledge types, responsible people and review dates: docs/design/knowledge-metadata.md.

// parseType reads a knowledge type; empty stays empty.
func parseType(s string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t != "" && !docs.ValidType(t) {
		return "", userErr("type must be rule, documentation, recommendation or experience.")
	}
	return t, nil
}

// parseReviewDue reads a review date (YYYY-MM-DD).
func parseReviewDue(s string) (*time.Time, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return nil, userErr("review_due must be a date (YYYY-MM-DD), or empty to remove it.")
	}
	return &d, nil
}

// readerUser resolves the e-mail of a person who is to become responsible for entries in a space:
// they must have logged in, must not be locked and must be able to read the space.
func (s *Server) readerUser(ctx context.Context, email string, spaceID uuid.UUID) (uuid.UUID, error) {
	u, err := s.knownUser(ctx, email)
	if err != nil {
		return uuid.Nil, err
	}
	if !u.Active {
		return uuid.Nil, userErr(fmt.Sprintf("%s is locked and cannot be made responsible.", u.Email))
	}
	roles, err := s.Checker.EffectiveRoles(ctx, u)
	if err != nil {
		return uuid.Nil, err
	}
	if !roles.Can(spaceID, authz.Reader) {
		return uuid.Nil, userErr(fmt.Sprintf("%s cannot read this space and so cannot be responsible for an entry in it.", u.Email))
	}
	return u.ID, nil
}

// originIO is a reference to the original a piece of knowledge was taken from. Atrium keeps the
// reference only; the assistant reads the original itself with the access of the user.
type originIO struct {
	Ref    string `json:"ref" jsonschema:"Address of the original, of the form scheme://... (for example https://... or sharepoint://...)"`
	Title  string `json:"title,omitempty" jsonschema:"Name of the original (file name, page title)"`
	ReadAt string `json:"read_at,omitempty" jsonschema:"Day (YYYY-MM-DD) the original was read"`
	SHA256 string `json:"sha256,omitempty" jsonschema:"Hash (hex) of the text taken over, to see later whether the original has changed"`
}

func originsIn(in []originIO) []docs.Origin {
	out := make([]docs.Origin, 0, len(in))
	for _, o := range in {
		out = append(out, docs.Origin{Ref: o.Ref, Title: o.Title, ReadAt: o.ReadAt, SHA256: o.SHA256})
	}
	return out
}

func originsOut(in []docs.Origin) []originIO {
	var out []originIO
	for _, o := range in {
		out = append(out, originIO{Ref: o.Ref, Title: o.Title, ReadAt: o.ReadAt, SHA256: o.SHA256})
	}
	return out
}
