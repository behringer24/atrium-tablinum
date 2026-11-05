package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// newsDefaultDays is how far back whats_new looks without a date.
const newsDefaultDays = 7

type newsIn struct {
	Since  string   `json:"since,omitempty" jsonschema:"Optional: report from this date (YYYY-MM-DD) or time (RFC 3339) on; default 7 days ago"`
	Spaces []string `json:"spaces,omitempty" jsonschema:"Optional: only these team spaces (slug or name)"`
	Limit  int      `json:"limit,omitempty" jsonschema:"Entries and skills each, default 20, at most 100"`
}

type newsEntry struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Space   string   `json:"space"`
	Change  string   `json:"change"` // new | updated
	Author  string   `json:"author"`
	Version int      `json:"version"`
	Updated string   `json:"updated"`
	Tags    []string `json:"tags"`
}

type newsSkill struct {
	Name        string `json:"name"`
	Space       string `json:"space"`
	Version     int    `json:"version"`
	Description string `json:"description"`
	Released    string `json:"released"`
}

type reviewWait struct {
	Space   string `json:"space"`
	Entries int    `json:"entries"`
	Skills  int    `json:"skills"`
}

type newsOut struct {
	Notice           string       `json:"notice,omitempty"`
	Message          string       `json:"message,omitempty"`
	Since            string       `json:"since"`
	Entries          []newsEntry  `json:"entries"`
	Skills           []newsSkill  `json:"skills"`
	WaitingForReview []reviewWait `json:"waiting_for_review,omitempty"`
}

// parseSince reads a date or a time; empty means newsDefaultDays ago.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now.AddDate(0, 0, -newsDefaultDays), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, s); err != nil {
			return time.Time{}, userErr("since must be a date (YYYY-MM-DD) or a time (RFC 3339).")
		}
	}
	if t.After(now) {
		return time.Time{}, userErr("since lies in the future.")
	}
	return t, nil
}

// whatsNew reports what became visible or changed in the team spaces the caller can read: approved
// entries and released skills, and for curators how many drafts wait in their spaces. Personal
// spaces are left out: news are what colleagues share.
func (s *Server) whatsNew(ctx context.Context, c *call, in newsIn) (newsOut, error) {
	a := c.Actor
	since, err := parseSince(in.Since, time.Now())
	if err != nil {
		return newsOut{}, err
	}
	team, err := s.teamSpacesAtLeast(ctx, a, authz.Reader)
	if err != nil {
		return newsOut{}, err
	}
	selected := team
	if len(in.Spaces) > 0 {
		selected = nil
		for _, ref := range in.Spaces {
			sp, cands := matchSpace(team, ref)
			switch {
			case sp != nil:
				selected = append(selected, *sp)
			case len(cands) > 1:
				return newsOut{}, userErr(fmt.Sprintf("The space %q is ambiguous: %s.", ref, spaceNames(cands)))
			default:
				return newsOut{}, userErr(fmt.Sprintf("Team space %q not found.", ref))
			}
		}
	}
	ids := make([]uuid.UUID, 0, len(selected))
	names := map[uuid.UUID]string{}
	for _, sp := range selected {
		ids = append(ids, sp.ID)
		names[sp.ID] = sp.Name
	}

	out := newsOut{Notice: untrustedNotice, Since: since.Format(time.RFC3339), Entries: []newsEntry{}, Skills: []newsSkill{}}
	changes, err := s.Docs.ChangedSince(ctx, a, ids, since, in.Limit)
	if err != nil {
		return newsOut{}, err
	}
	for _, ch := range changes {
		kind := "updated"
		if ch.New {
			kind = "new"
		}
		tags := ch.Tags
		if tags == nil {
			tags = []string{}
		}
		out.Entries = append(out.Entries, newsEntry{ID: ch.ID.String(), Title: ch.Title, Space: names[ch.SpaceID], Change: kind,
			Author: ch.AuthorEmail, Version: ch.Version, Updated: ch.UpdatedAt.Format(time.RFC3339), Tags: tags})
	}
	waiting := map[uuid.UUID]*reviewWait{}
	wait := func(id uuid.UUID) *reviewWait {
		if waiting[id] == nil {
			waiting[id] = &reviewWait{Space: names[id]}
		}
		return waiting[id]
	}
	drafts, err := s.Docs.DraftCounts(ctx, a, ids)
	if err != nil {
		return newsOut{}, err
	}
	for id, n := range drafts {
		wait(id).Entries = n
	}
	if s.Skills != nil {
		releases, err := s.Skills.ReleasedSince(ctx, a, ids, since, in.Limit)
		if err != nil {
			return newsOut{}, err
		}
		for _, r := range releases {
			out.Skills = append(out.Skills, newsSkill{Name: r.Name, Space: names[r.SpaceID], Version: r.Version,
				Description: r.Description, Released: r.PublishedAt.Format(time.RFC3339)})
		}
		skillDrafts, err := s.Skills.DraftCounts(ctx, a, ids)
		if err != nil {
			return newsOut{}, err
		}
		for id, n := range skillDrafts {
			wait(id).Skills = n
		}
	}
	for _, sp := range selected { // in the order of the spaces
		if w := waiting[sp.ID]; w != nil {
			out.WaitingForReview = append(out.WaitingForReview, *w)
		}
	}
	if len(out.Entries) == 0 && len(out.Skills) == 0 && len(out.WaitingForReview) == 0 {
		out.Message = fmt.Sprintf("Nothing new in %s since %s.", spacesPhrase(selected), since.Format("2006-01-02"))
	}
	return out, nil
}

func spacesPhrase(list []spaces.Space) string {
	if len(list) == 1 {
		return "the space " + list[0].Name
	}
	return "your team spaces"
}
