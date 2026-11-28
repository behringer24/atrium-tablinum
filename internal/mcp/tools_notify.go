package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/behringer24/atrium-tablinum/internal/notify"
)

type notificationsIn struct {
	Action     string   `json:"action" jsonschema:"get, set or test"`
	Connection string   `json:"connection,omitempty" jsonschema:"set and test: the name of the connection, as get lists it"`
	Enabled    *bool    `json:"enabled,omitempty" jsonschema:"set: true sends the chosen events through the connection, false stops it"`
	Events     []string `json:"events,omitempty" jsonschema:"set: the events that go through the connection; the list replaces the old one (see get for the events)"`
	Mode       string   `json:"mode,omitempty" jsonschema:"set: instant (a notice follows within about a minute, notices of one kind are merged) or digest (one message per person and day)"`
	DigestAt   string   `json:"digest_at,omitempty" jsonschema:"set: the time of day (UTC, HH:MM) the digest is sent"`
	SpaceNames *bool    `json:"space_names,omitempty" jsonschema:"set: true lets a message of a web hook name the space. Off by default: the readers of a chat channel may not be members of the space"`
	Confirm    bool     `json:"confirm,omitempty" jsonschema:"set and test: true only after confirmation by the user, who was told what changes or where the test goes"`
}

type notificationConn struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`  // slack, webhook or smtp
	State      string   `json:"state"` // on, off or not_used
	Events     []string `json:"events"`
	Mode       string   `json:"mode"`
	DigestAt   string   `json:"digest_at,omitempty"`
	SpaceNames bool     `json:"space_names"`
	Pending    int      `json:"pending"`
	Sent24h    int      `json:"sent_last_day"`
	Failed24h  int      `json:"given_up_last_day"`
	LastError  string   `json:"last_error,omitempty"`
}

type notificationsOut struct {
	Message     string             `json:"message,omitempty"`
	Connections []notificationConn `json:"connections"`
	// NotUsed names connections that are in config.yaml but not used by this edition.
	NotUsed []string          `json:"not_used,omitempty"`
	Events  []notify.KindInfo `json:"events,omitempty"`
}

func (s *Server) adminNotifications(ctx context.Context, c *call, in notificationsIn) (notificationsOut, error) {
	if !s.Notify.Enabled() {
		return notificationsOut{Connections: []notificationConn{}, Message: "Notifications are off. An instance admin turns them on in config.yaml " +
			"(notifications.enabled and at least one connection under notifications.connections, then a restart). " +
			"What goes through a connection is chosen here afterwards."}, nil
	}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "get":
		return s.notificationsGet(ctx, "")
	case "set":
		return s.notificationsSet(ctx, c, in)
	case "test":
		return s.notificationsTest(ctx, c, in)
	}
	return notificationsOut{}, userErr("action must be get, set or test.")
}

func (s *Server) notificationsGet(ctx context.Context, msg string) (notificationsOut, error) {
	settings, err := s.Notify.Settings(ctx)
	if err != nil {
		return notificationsOut{}, err
	}
	stats, err := s.Notify.Stats(ctx)
	if err != nil {
		return notificationsOut{}, err
	}
	active, ignored := s.Notify.Connections()
	out := notificationsOut{Message: msg, Connections: []notificationConn{}, NotUsed: ignored, Events: notify.Kinds}
	for _, c := range active {
		st, ss := settings[c.Name], stats[c.Name]
		state := "off"
		if st.Enabled {
			state = "on"
		}
		nc := notificationConn{Name: c.Name, Type: c.Type, State: state, Events: []string{}, Mode: st.Mode, SpaceNames: st.SpaceNames,
			Pending: ss.Pending, Sent24h: ss.Sent24h, Failed24h: ss.Failed24h, LastError: ss.LastError}
		if st.Mode == "digest" {
			nc.DigestAt = st.DigestAt
		}
		for _, e := range st.Events {
			nc.Events = append(nc.Events, string(e))
		}
		out.Connections = append(out.Connections, nc)
	}
	if len(ignored) > 0 {
		out.Message = strings.TrimSpace(msg + " The Community edition uses one web hook and one SMTP connection: the others in config.yaml are not used (" +
			strings.Join(ignored, ", ") + ").")
	}
	return out, nil
}

func (s *Server) notificationsSet(ctx context.Context, c *call, in notificationsIn) (notificationsOut, error) {
	name := strings.TrimSpace(in.Connection)
	if name == "" {
		return notificationsOut{}, userErr("Name the connection (see get).")
	}
	p := notify.Patch{Enabled: in.Enabled, SpaceNames: in.SpaceNames}
	if in.Events != nil {
		p.Events = &in.Events
	}
	if m := strings.TrimSpace(in.Mode); m != "" {
		p.Mode = &m
	}
	if d := strings.TrimSpace(in.DigestAt); d != "" {
		p.DigestAt = &d
	}
	if p == (notify.Patch{}) {
		return notificationsOut{}, userErr("Give at least one of enabled, events, mode, digest_at or space_names.")
	}
	if !in.Confirm {
		return notificationsOut{}, userErr(fmt.Sprintf("Changing the connection %s decides who is told what, and where. Tell the user the new values and ask for confirmation, then call again with confirm=true.", name))
	}
	before, after, err := s.Notify.SetSetting(ctx, name, p, c.Actor.User.ID)
	if err != nil {
		return notificationsOut{}, userErr(err.Error())
	}
	s.record(ctx, c.Actor, "admin_notifications_set", nil, map[string]any{"connection": name,
		"enabled": map[string]any{"from": before.Enabled, "to": after.Enabled},
		"events":  map[string]any{"from": kinds(before.Events), "to": kinds(after.Events)},
		"mode":    after.Mode, "space_names": after.SpaceNames})
	return s.notificationsGet(ctx, fmt.Sprintf("The connection %s is updated and applies to the next notice.", name))
}

func (s *Server) notificationsTest(ctx context.Context, c *call, in notificationsIn) (notificationsOut, error) {
	name := strings.TrimSpace(in.Connection)
	if name == "" {
		return notificationsOut{}, userErr("Name the connection (see get).")
	}
	if !in.Confirm {
		return notificationsOut{}, userErr(fmt.Sprintf("A test sends a notice through %s now: a mail goes to the user's own address, a web hook posts to its channel. Tell the user, then call again with confirm=true.", name))
	}
	if err := s.Notify.Test(ctx, name, c.Actor.User.Email); err != nil {
		if err == notify.ErrUnknownConnection {
			return notificationsOut{}, userErr(err.Error())
		}
		return notificationsOut{}, userErr("The test failed: " + err.Error())
	}
	s.record(ctx, c.Actor, "admin_notifications_test", nil, map[string]any{"connection": name})
	return s.notificationsGet(ctx, "The test notice was sent. Ask the user whether it arrived.")
}

func kinds(ks []notify.Kind) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, string(k))
	}
	return out
}
