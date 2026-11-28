package notify

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Stats says how deliveries through one connection are going.
type Stats struct {
	Pending       int           // waiting to be sent or to be tried again
	Sent24h       int           // sent in the last day
	Failed24h     int           // given up in the last day
	OldestPending time.Duration // how long the oldest due delivery has been waiting
	LastError     string        // the last error of a delivery that is waiting or was given up
}

// Stats returns the figures of every connection in use.
func (s *Service) Stats(ctx context.Context) (map[string]Stats, error) {
	out := map[string]Stats{}
	for _, c := range s.inUse() {
		out[c.Name] = Stats{}
	}
	rows, err := s.pool.Query(ctx, `SELECT connection,
		count(*) FILTER (WHERE status IN ('pending', 'sending')),
		count(*) FILTER (WHERE status = 'sent' AND sent_at > now() - interval '1 day'),
		count(*) FILTER (WHERE status = 'failed' AND created_at > now() - interval '1 day'),
		COALESCE(EXTRACT(EPOCH FROM now() - min(send_after) FILTER (WHERE status IN ('pending', 'sending') AND send_after < now())), 0),
		COALESCE((array_agg(last_error ORDER BY created_at DESC) FILTER (WHERE last_error IS NOT NULL))[1], '')
		FROM notification_deliveries GROUP BY connection`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var st Stats
		var oldest float64
		if err := rows.Scan(&name, &st.Pending, &st.Sent24h, &st.Failed24h, &oldest, &st.LastError); err != nil {
			return nil, err
		}
		st.OldestPending = time.Duration(oldest * float64(time.Second))
		if _, ok := out[name]; ok {
			out[name] = st
		}
	}
	return out, rows.Err()
}

// Summary is the one line of the health check: an error text when something needs a look.
func (s *Service) Summary(ctx context.Context) (string, error) {
	if !s.Enabled() {
		return "off", nil
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		return "", err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return "", err
	}
	active := s.inUse()
	var on, pending int
	var problem string
	for _, c := range active {
		st := stats[c.Name]
		if settings[c.Name].Enabled {
			on++
		}
		pending += st.Pending
		switch {
		case st.Failed24h > 0:
			problem = c.Name + ": " + itoa(st.Failed24h) + " notices given up in the last day (" + st.LastError + ")"
		case st.OldestPending > 15*time.Minute && st.LastError != "":
			problem = c.Name + ": notices have been waiting for " + st.OldestPending.Round(time.Minute).String() + " (" + st.LastError + ")"
		}
		if problem != "" {
			return "", errors.New(problem)
		}
	}
	return itoa(on) + " of " + itoa(len(active)) + " connections on, " + itoa(pending) + " notices waiting", nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Test sends a test notice through a connection now. A mail goes to the address given (the caller's own),
// a web hook to its address. It returns the error text the receiver gave, scrubbed of addresses.
func (s *Service) Test(ctx context.Context, name, toEmail string) error {
	c, ok := s.connection(name)
	if !ok {
		return ErrUnknownConnection
	}
	d := delivery{id: uuid.New(), connection: name, kind: kindTest, count: 1}
	if c.Type == "smtp" {
		msg := buildMail(s.now(), s.opt.Instance, c.From, toEmail, s.opt.Instance+": test notice", "- "+line(d, "")+"\r\n", "Atrium")
		return s.mail(ctx, c, toEmail, msg)
	}
	return s.sendWebhook(ctx, c, d, false)
}
