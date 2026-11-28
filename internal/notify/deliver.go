package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/behringer24/atrium-tablinum/internal/authz"
)

const (
	// leaseTime is how long a claimed delivery belongs to the sender; a crash leaves it to be claimed again.
	leaseTime = 5 * time.Minute
	// maxAttempts is the number of tries before a delivery is given up.
	maxAttempts = 6
	// batchSize caps the deliveries claimed in one run, perConnection those of one web hook in a run, so
	// that a broken or slow target cannot hold up the others.
	batchSize     = 100
	perConnection = 20
	retention     = 30 * 24 * time.Hour
)

var backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}

func retryAfter(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(backoff) {
		attempts = len(backoff)
	}
	return backoff[attempts-1]
}

// Run is the periodic task: route new events, send what is due, clean up.
func (s *Service) Run(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	if err := s.route(ctx); err != nil {
		return err
	}
	if err := s.send(ctx); err != nil {
		return err
	}
	return s.cleanup(ctx)
}

type eventRow struct {
	id        uuid.UUID
	ev        Event
	createdAt time.Time
}

// route turns the new events into deliveries, merging them into a delivery of the same kind that has not
// been sent yet.
func (s *Service) route(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, space_id, subject_id, subject_kind, actor_id, recipient_id, detail, created_at
		FROM notification_events WHERE routed_at IS NULL ORDER BY created_at LIMIT 500`)
	if err != nil {
		return err
	}
	var evs []eventRow
	for rows.Next() {
		var e eventRow
		var kind string
		if err := rows.Scan(&e.id, &kind, &e.ev.SpaceID, &e.ev.SubjectID, &e.ev.SubjectKind, &e.ev.ActorID, &e.ev.RecipientID, &e.ev.Detail, &e.createdAt); err != nil {
			rows.Close()
			return err
		}
		e.ev.Kind = Kind(kind)
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(evs) == 0 {
		return nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	all, err := s.users.List(ctx)
	if err != nil {
		return err
	}
	ppl := &people{all: all, roles: map[uuid.UUID]authz.Roles{}}
	for _, e := range evs {
		planned, err := s.router.Route(ctx, RoutedEvent{ID: e.id, Event: e.ev, CreatedAt: e.createdAt, settings: settings, people: ppl})
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		for _, p := range planned {
			if err := addDelivery(ctx, tx, e, p); err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE notification_events SET routed_at = now() WHERE id = $1`, e.id); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func addDelivery(ctx context.Context, tx pgx.Tx, e eventRow, p Planned) error {
	space := uuid.Nil
	if e.ev.SpaceID != nil {
		space = *e.ev.SpaceID
	}
	tag, err := tx.Exec(ctx, `UPDATE notification_deliveries SET count = count + 1
		WHERE status = 'pending' AND connection = $1 AND kind = $2 AND space_id = $3 AND recipient_id = $4 AND detail = $5
		  AND send_after > now()`,
		p.Connection, string(e.ev.Kind), space, p.Recipient, e.ev.Detail)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO notification_deliveries (connection, kind, space_id, recipient_id, detail, send_after)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		p.Connection, string(e.ev.Kind), space, p.Recipient, e.ev.Detail, p.SendAfter)
	return err
}

type delivery struct {
	id         uuid.UUID
	connection string
	kind       Kind
	space      uuid.UUID
	recipient  uuid.UUID
	detail     string
	count      int
	attempts   int
}

// send claims what is due and sends it.
func (s *Service) send(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `UPDATE notification_deliveries SET status = 'sending', attempts = attempts + 1, send_after = now() + $1::interval
		WHERE id IN (SELECT id FROM notification_deliveries WHERE status IN ('pending', 'sending') AND send_after <= now()
		             ORDER BY send_after LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING id, connection, kind, space_id, recipient_id, detail, count, attempts`,
		fmt.Sprintf("%d seconds", int(leaseTime.Seconds())), batchSize)
	if err != nil {
		return err
	}
	var due []delivery
	for rows.Next() {
		var d delivery
		var kind string
		if err := rows.Scan(&d.id, &d.connection, &kind, &d.space, &d.recipient, &d.detail, &d.count, &d.attempts); err != nil {
			rows.Close()
			return err
		}
		d.kind = Kind(kind)
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	// A mail names everything due for one person in one message; a web hook gets one call for each delivery.
	type mailKey struct {
		conn string
		to   uuid.UUID
	}
	mails := map[mailKey][]delivery{}
	var mailOrder []mailKey
	perConn := map[string]int{}
	for _, d := range due {
		c, ok := s.connection(d.connection)
		if !ok {
			s.finish(ctx, []delivery{d}, errors.New("the connection is not in use: it is not in the configuration or this edition does not use it"), true)
			continue
		}
		if c.Type == "smtp" {
			k := mailKey{d.connection, d.recipient}
			if _, seen := mails[k]; !seen {
				mailOrder = append(mailOrder, k)
			}
			mails[k] = append(mails[k], d)
			continue
		}
		if perConn[d.connection] >= perConnection {
			s.release(ctx, d) // left for the next run
			continue
		}
		perConn[d.connection]++
		err := s.sendWebhook(ctx, c, d, settings[c.Name].SpaceNames)
		s.settle(ctx, []delivery{d}, err)
	}
	for _, k := range mailOrder {
		c, _ := s.connection(k.conn)
		s.settle(ctx, mails[k], s.sendMailTo(ctx, c, k.to, mails[k]))
	}
	return nil
}

// permanentError is a failure that retrying does not help.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// retryError asks for the next attempt at a given time (a web hook answered 429 with Retry-After).
type retryError struct {
	msg   string
	after time.Duration
}

func (e *retryError) Error() string { return e.msg }

func (s *Service) settle(ctx context.Context, ds []delivery, err error) {
	if err == nil {
		s.finish(ctx, ds, nil, false)
		return
	}
	var perm *permanentError
	s.finish(ctx, ds, err, errors.As(err, &perm))
}

// finish marks the deliveries sent, or, after an error, schedules the next attempt or gives them up.
func (s *Service) finish(ctx context.Context, ds []delivery, err error, permanent bool) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, d := range ds {
		var qerr error
		switch {
		case err == nil:
			_, qerr = s.pool.Exec(fctx, `UPDATE notification_deliveries SET status = 'sent', sent_at = now(), last_error = NULL WHERE id = $1`, d.id)
		case permanent || d.attempts >= maxAttempts:
			slog.Warn("Notification given up", "connection", d.connection, "event", d.kind, "error", err)
			_, qerr = s.pool.Exec(fctx, `UPDATE notification_deliveries SET status = 'failed', last_error = $2 WHERE id = $1`, d.id, clip(err.Error()))
		default:
			wait := retryAfter(d.attempts)
			var re *retryError
			if errors.As(err, &re) && re.after > wait {
				wait = re.after
			}
			slog.Warn("Notification not delivered, will try again", "connection", d.connection, "event", d.kind, "attempt", d.attempts, "error", err)
			_, qerr = s.pool.Exec(fctx, `UPDATE notification_deliveries SET status = 'pending', last_error = $2, send_after = now() + $3::interval WHERE id = $1`,
				d.id, clip(err.Error()), fmt.Sprintf("%d seconds", int(wait.Seconds())))
		}
		if qerr != nil {
			slog.Error("Notification state not saved", "error", qerr)
		}
	}
}

// release puts a claimed delivery back without counting an attempt.
func (s *Service) release(ctx context.Context, d delivery) {
	_, _ = s.pool.Exec(ctx, `UPDATE notification_deliveries SET status = 'pending', attempts = attempts - 1, send_after = now() WHERE id = $1`, d.id)
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// scrub removes the address from a network error: for a Slack web hook the address is the secret.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (s *Service) sendMailTo(ctx context.Context, c Connection, to uuid.UUID, ds []delivery) error {
	u, err := s.users.Get(ctx, to)
	if err != nil || !u.Active || u.Email == "" {
		return &permanentError{"the recipient is no longer an active account"}
	}
	msg, err := s.mailMessage(ctx, c, u, ds)
	if err != nil {
		return err
	}
	return s.mail(ctx, c, u.Email, msg)
}

// cleanup deletes what was sent or given up, and events that were routed, after the retention period.
func (s *Service) cleanup(ctx context.Context) error {
	s.mu.Lock()
	due := s.now().Sub(s.lastCleanup) > time.Hour
	if due {
		s.lastCleanup = s.now()
	}
	s.mu.Unlock()
	if !due {
		return nil
	}
	cut := s.now().Add(-retention)
	if _, err := s.pool.Exec(ctx, `DELETE FROM notification_deliveries WHERE status IN ('sent', 'failed') AND created_at < $1`, cut); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM notification_events WHERE routed_at IS NOT NULL AND created_at < $1`, cut)
	return err
}
