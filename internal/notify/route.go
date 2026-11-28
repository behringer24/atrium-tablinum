package notify

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// mergeWindow is how long an instant notice waits for more of its kind before it is sent as one.
const mergeWindow = time.Minute

// Planned is one delivery a router decided on.
type Planned struct {
	Connection string
	// Recipient is the person a mail goes to; uuid.Nil for a web hook.
	Recipient uuid.UUID
	SendAfter time.Time
}

// RoutedEvent is an event as the router sees it.
type RoutedEvent struct {
	ID        uuid.UUID
	Event     Event
	CreatedAt time.Time
	settings  map[string]Setting
	people    *people
}

// Router decides where an event goes. It knows nothing about sending, and the code that emits events
// knows nothing about routing, so the way events find their connections can change without touching
// either.
type Router interface {
	Route(ctx context.Context, ev RoutedEvent) ([]Planned, error)
}

// globalRouter is the built-in one: every connection an instance admin switched on gets the events
// the admin chose for it; a mail goes to the people the event names by their role.
type globalRouter struct{ s *Service }

func (r globalRouter) Route(ctx context.Context, ev RoutedEvent) ([]Planned, error) {
	var out []Planned
	var recipients []uuid.UUID
	resolved := false
	for _, c := range r.s.inUse() {
		st := ev.settings[c.Name]
		if !st.Enabled || !hasEvent(st.Events, ev.Event.Kind) {
			continue
		}
		at := ev.CreatedAt.Add(mergeWindow)
		if st.Mode == "digest" {
			at = nextDigest(ev.CreatedAt, st.DigestAt)
		}
		if c.Type != "smtp" {
			out = append(out, Planned{Connection: c.Name, SendAfter: at})
			continue
		}
		if !resolved {
			var err error
			if recipients, err = r.s.recipientsOf(ctx, ev); err != nil {
				return nil, err
			}
			resolved = true
		}
		for _, id := range recipients {
			out = append(out, Planned{Connection: c.Name, Recipient: id, SendAfter: at})
		}
	}
	return out, nil
}

func hasEvent(list []Kind, k Kind) bool {
	for _, e := range list {
		if e == k {
			return true
		}
	}
	return false
}

// recipientsOf names the people who get a mail about the event, by their role at this moment. The person
// who caused the event is not told about their own action; locked accounts and service accounts get nothing.
func (s *Service) recipientsOf(ctx context.Context, ev RoutedEvent) ([]uuid.UUID, error) {
	var cands []users.User
	switch ev.Event.Kind {
	case DraftApproved, DraftReturned, ItemReceived:
		if ev.Event.RecipientID == nil {
			return nil, nil
		}
		u, err := s.users.Get(ctx, *ev.Event.RecipientID)
		if err != nil {
			return nil, nil // the account is gone: nobody to tell
		}
		cands = []users.User{u}
	case HealthChanged:
		for _, u := range ev.people.all {
			if u.InstanceAdmin {
				cands = append(cands, u)
			}
		}
	case DraftWaiting:
		if ev.Event.SpaceID == nil {
			return nil, nil
		}
		for _, u := range ev.people.all {
			if !u.Active || u.IsService() {
				continue
			}
			roles, err := ev.people.rolesOf(ctx, s, u)
			if err != nil {
				return nil, err
			}
			if roles.Can(*ev.Event.SpaceID, authz.Curator) {
				cands = append(cands, u)
			}
		}
	}
	var out []uuid.UUID
	for _, u := range cands {
		if !u.Active || u.IsService() || u.Email == "" {
			continue
		}
		if ev.Event.ActorID != nil && *ev.Event.ActorID == u.ID {
			continue
		}
		out = append(out, u.ID)
	}
	return out, nil
}

// people caches the accounts and their roles for one run of the routing.
type people struct {
	all   []users.User
	roles map[uuid.UUID]authz.Roles
}

func (p *people) rolesOf(ctx context.Context, s *Service, u users.User) (authz.Roles, error) {
	if r, ok := p.roles[u.ID]; ok {
		return r, nil
	}
	r, err := s.checker.EffectiveRoles(ctx, u)
	if err != nil {
		return nil, err
	}
	p.roles[u.ID] = r
	return r, nil
}
