package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type reembedIn struct {
	Action  string `json:"action,omitempty" jsonschema:"status (default), start or stop"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"start and stop: true only after confirmation by the user"`
}

type reembedOut struct {
	Message string `json:"message,omitempty"`
	// Model is the configured model; Sections all sections, Stale those embedded with another model
	// or without a vector, which search finds by full text only.
	Model    string         `json:"model"`
	Sections int            `json:"sections"`
	Stale    int            `json:"stale"`
	ByModel  map[string]int `json:"by_model"`
	// Requested is set while a rebuild is under way.
	Requested string `json:"requested_since,omitempty"`
}

func (s *Server) adminReembed(ctx context.Context, c *call, in reembedIn) (reembedOut, error) {
	st, err := s.Reembed.Status(ctx)
	if err != nil {
		return reembedOut{}, err
	}
	out := reembedOut{Model: st.Model, Sections: st.Total, Stale: st.Stale, ByModel: st.ByModel}
	if st.Requested != nil {
		out.Requested = st.Requested.Format(time.RFC3339)
	}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "", "status":
		switch {
		case st.Stale == 0:
			out.Message = "Every section is embedded with the configured model."
		case st.Requested != nil:
			out.Message = fmt.Sprintf("Re-embedding is under way: %d of %d sections are left. They are found by full text only until then.", st.Stale, st.Total)
		default:
			out.Message = fmt.Sprintf("%d of %d sections are not embedded with %s and are found by full text only. Start the rebuild with action=start.",
				st.Stale, st.Total, st.Model)
		}
		return out, nil
	case "start":
		if st.Stale == 0 {
			out.Message = "Nothing to do: every section is embedded with the configured model."
			return out, nil
		}
		if !in.Confirm {
			return reembedOut{}, userErr(fmt.Sprintf("Re-embedding sends %d sections to the embedding service (%s) again, in the background, "+
				"batch by batch; with a paid service this costs as much as saving them did. Search keeps working meanwhile. "+
				"Tell the user, then call again with confirm=true.", st.Stale, st.Model))
		}
		if err := s.Reembed.Request(ctx); err != nil {
			return reembedOut{}, err
		}
		s.record(ctx, c.Actor, "reembed_start", nil, map[string]any{"model": st.Model, "sections": st.Stale})
		out.Requested = time.Now().UTC().Format(time.RFC3339)
		out.Message = fmt.Sprintf("Re-embedding of %d sections has started in the background. action=status shows how far it is; admin_health shows errors.", st.Stale)
		return out, nil
	case "stop":
		if !in.Confirm {
			return reembedOut{}, userErr("Stopping leaves the sections not yet rebuilt to full-text search. Ask the user, then call again with confirm=true.")
		}
		stopped, err := s.Reembed.Cancel(ctx)
		if err != nil {
			return reembedOut{}, err
		}
		out.Requested = ""
		out.Message = "No re-embedding was under way."
		if stopped {
			s.record(ctx, c.Actor, "reembed_stop", nil, map[string]any{"sections_left": st.Stale})
			out.Message = fmt.Sprintf("Re-embedding stopped; %d sections are still left. action=start continues where it stopped.", st.Stale)
		}
		return out, nil
	}
	return reembedOut{}, userErr("action must be status, start or stop.")
}
