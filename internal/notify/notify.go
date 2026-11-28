// Package notify tells people that something waits for them (docs/design/notifications.md). A change writes
// an event in its own transaction (Emit); a periodic task routes the events to the connections an instance
// admin switched on, collects what is to be sent in an outbox and delivers it with retries.
//
// A notice points to what waits and carries no content: no title, no text, no note. The connections
// (addresses, secrets) come from config.yaml; nothing here can create or change one.
package notify

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// Kind is what happened.
type Kind string

const (
	DraftWaiting  Kind = "draft_waiting"  // an entry or a skill entered review: told are the curators of the space
	DraftApproved Kind = "draft_approved" // a curator approved a draft: told is its author
	DraftReturned Kind = "draft_returned" // a curator returned a draft: told is its author
	ItemReceived  Kind = "item_received"  // something was sent to a personal space: told is its owner
	HealthChanged Kind = "health_changed" // a check of admin_health changed state: told are the instance admins
)

// KindInfo describes an event for the admin tool.
type KindInfo struct {
	Kind        Kind   `json:"event"`
	Description string `json:"description"`
}

// Kinds are all events, in the order they are listed.
var Kinds = []KindInfo{
	{DraftWaiting, "A draft of an entry or skill waits for review. Told: the curators and admins of the space."},
	{DraftApproved, "A draft was approved. Told: its author."},
	{DraftReturned, "A draft was returned to its author. Told: the author."},
	{ItemReceived, "Somebody sent an entry or skill to a personal space. Told: its owner."},
	{HealthChanged, "A check of admin_health changed between ok and failing. Told: the instance admins."},
}

// ValidKind reports whether k is an event.
func ValidKind(k string) bool {
	for _, i := range Kinds {
		if string(i.Kind) == k {
			return true
		}
	}
	return false
}

// Event is one thing that happened. It names facts by id, never by title or text.
type Event struct {
	Kind        Kind
	SpaceID     *uuid.UUID
	SubjectID   *uuid.UUID
	SubjectKind string // knowledge or skill
	ActorID     *uuid.UUID
	// RecipientID is the person the event is about (the author, the owner of a personal space); nil for
	// events about a space or the instance.
	RecipientID *uuid.UUID
	// Detail is a short fact that is no content: the name of a health check and its state.
	Detail string
}

// Execer is satisfied by pgxpool.Pool and pgx.Tx, so an event joins the transaction of the change.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Emitter is what the write paths hold. A nil Emitter means notifications are not wired in.
type Emitter interface {
	Emit(ctx context.Context, db Execer, ev Event) error
}

// Emit passes the event on if there is an emitter.
func Emit(ctx context.Context, e Emitter, db Execer, ev Event) error {
	if e == nil {
		return nil
	}
	return e.Emit(ctx, db, ev)
}

// Connection is a place a notice goes, taken over from config.yaml.
type Connection struct {
	Name, Type  string // slack, webhook or smtp
	URL, Secret string
	Host        string
	Port        int
	TLS         string
	Username    string
	Password    string
	From        string
}

// Options is what the server hands to New.
type Options struct {
	Enabled bool
	// Connections as configured, in order. The first MaxWebhooks web hooks and the first MaxMail SMTP
	// connections are used (0 = all); the others are ignored and reported as such.
	Connections []Connection
	MaxWebhooks int
	MaxMail     int
	// Limits, if set, is asked whenever the connections in use are needed and replaces MaxWebhooks and
	// MaxMail, so that a change of the edition (a licence installed or ended) applies at once.
	Limits func() (webhooks, mail int)
	// AllowPrivateNetworks lets a web hook reach private addresses.
	AllowPrivateNetworks bool
	// Instance is the name shown in a notice.
	Instance string
	// Mailer replaces the SMTP delivery; for tests.
	Mailer func(ctx context.Context, c Connection, to string, msg []byte) error
}

// Service routes and delivers notices.
type Service struct {
	pool    *pgxpool.Pool
	users   *users.Store
	checker *authz.Checker
	spaces  *spaces.Store
	opt     Options
	router  Router

	http *http.Client
	// mail sends one message; replaced in tests.
	mail func(ctx context.Context, c Connection, to string, msg []byte) error
	now  func() time.Time

	mu          sync.Mutex
	lastCleanup time.Time
	health      map[string]bool // check name -> ok, for WatchHealth
}

// New builds the service. It never fails: a connection that cannot be used is reported, not fatal.
func New(pool *pgxpool.Pool, u *users.Store, checker *authz.Checker, sp *spaces.Store, opt Options) *Service {
	s := &Service{pool: pool, users: u, checker: checker, spaces: sp, opt: opt, now: time.Now}
	s.router = globalRouter{s}
	s.http = newHTTPClient(opt.AllowPrivateNetworks)
	s.mail = sendSMTP
	if opt.Mailer != nil {
		s.mail = opt.Mailer
	}
	return s
}

// Enabled reports whether notices are sent at all.
func (s *Service) Enabled() bool { return s != nil && s.opt.Enabled && len(s.inUse()) > 0 }

// Emit records an event in the transaction of the change that caused it. It writes nothing while
// notifications are off, so an instance that does not use them keeps no rows.
func (s *Service) Emit(ctx context.Context, db Execer, ev Event) error {
	if !s.Enabled() {
		return nil
	}
	_, err := db.Exec(ctx, `INSERT INTO notification_events (kind, space_id, subject_id, subject_kind, actor_id, recipient_id, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(ev.Kind), ev.SpaceID, ev.SubjectID, ev.SubjectKind, ev.ActorID, ev.RecipientID, ev.Detail)
	return err
}

// Connections returns the connections in use and the names of those left out.
func (s *Service) Connections() (active []Connection, ignored []string) {
	return s.resolve()
}

// resolve applies the limits of the edition: the first web hooks and the first SMTP connections in the order
// of the file are in use (0 = all), the others are left out.
func (s *Service) resolve() (active []Connection, ignored []string) {
	maxWebhooks, maxMail := s.opt.MaxWebhooks, s.opt.MaxMail
	if s.opt.Limits != nil {
		maxWebhooks, maxMail = s.opt.Limits()
	}
	var webhooks, mail int
	for _, c := range s.opt.Connections {
		switch c.Type {
		case "smtp":
			if maxMail > 0 && mail >= maxMail {
				ignored = append(ignored, c.Name)
				continue
			}
			mail++
		default:
			if maxWebhooks > 0 && webhooks >= maxWebhooks {
				ignored = append(ignored, c.Name)
				continue
			}
			webhooks++
		}
		active = append(active, c)
	}
	return active, ignored
}

// inUse is the connections in use now.
func (s *Service) inUse() []Connection {
	active, _ := s.resolve()
	return active
}

// connection finds an active connection by name.
func (s *Service) connection(name string) (Connection, bool) {
	for _, c := range s.inUse() {
		if c.Name == name {
			return c, true
		}
	}
	return Connection{}, false
}
