// Package jobs runs background work: periodic maintenance tasks and queued jobs from the
// jobs table (claimed with FOR UPDATE SKIP LOCKED). One instance serves one customer, so
// there is no cross-instance coordination beyond the row locks.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// Periodic is a maintenance task run at start and then every Every.
type Periodic struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context) error
}

// Job is a row claimed from the queue.
type Job struct {
	ID     uuid.UUID
	Kind   string
	Params json.RawMessage
}

// Handler processes one queued job; its error text is stored on failure.
type Handler func(ctx context.Context, j Job) error

// PeriodicStatus reports how a periodic task did last.
type PeriodicStatus struct {
	Name     string
	LastRun  time.Time
	Duration time.Duration
	LastErr  string
}

type Runner struct {
	pool     *pgxpool.Pool
	log      *slog.Logger
	poll     time.Duration
	periodic []Periodic
	handlers map[string]Handler

	mu     sync.Mutex
	status map[string]PeriodicStatus
	wg     sync.WaitGroup
}

func New(pool *pgxpool.Pool, poll time.Duration) *Runner {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	return &Runner{pool: pool, log: slog.Default(), poll: poll, handlers: map[string]Handler{}, status: map[string]PeriodicStatus{}}
}

func (r *Runner) AddPeriodic(p Periodic)        { r.periodic = append(r.periodic, p) }
func (r *Runner) Handle(kind string, h Handler) { r.handlers[kind] = h }

// Start launches the workers. They stop when ctx ends; Wait blocks until they have.
func (r *Runner) Start(ctx context.Context) error {
	// A crash can leave rows in "running"; nothing else would ever pick them up.
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET status = 'failed', error = 'durch Neustart unterbrochen', finished_at = now()
		WHERE status = 'running'`); err != nil {
		return err
	}
	for _, p := range r.periodic {
		r.wg.Add(1)
		go r.runPeriodic(ctx, p)
	}
	r.wg.Add(1)
	go r.runQueue(ctx)
	return nil
}

func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) runPeriodic(ctx context.Context, p Periodic) {
	defer r.wg.Done()
	t := time.NewTicker(p.Every)
	defer t.Stop()
	for {
		r.runOnce(ctx, p)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Runner) runOnce(ctx context.Context, p Periodic) {
	start := time.Now()
	err := safely(func() error { return p.Run(ctx) })
	st := PeriodicStatus{Name: p.Name, LastRun: start, Duration: time.Since(start)}
	if err != nil && ctx.Err() == nil {
		st.LastErr = err.Error()
		r.log.Error("background task failed", "task", p.Name, "error", err)
	}
	r.mu.Lock()
	r.status[p.Name] = st
	r.mu.Unlock()
}

// PeriodicStatus returns the last result of every periodic task.
func (r *Runner) PeriodicStatus() []PeriodicStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PeriodicStatus, 0, len(r.periodic))
	for _, p := range r.periodic {
		if st, ok := r.status[p.Name]; ok {
			out = append(out, st)
		} else {
			out = append(out, PeriodicStatus{Name: p.Name})
		}
	}
	return out
}

func (r *Runner) runQueue(ctx context.Context) {
	defer r.wg.Done()
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		for { // drain
			ok, err := r.processOne(ctx)
			if err != nil {
				r.log.Error("job queue", "error", err)
			}
			if !ok || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// processOne claims and runs one queued job; it reports whether there was one.
func (r *Runner) processOne(ctx context.Context) (bool, error) {
	var j Job
	err := r.pool.QueryRow(ctx, `
UPDATE jobs SET status = 'running', started_at = now()
WHERE id = (SELECT id FROM jobs WHERE status = 'queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING id, kind, COALESCE(params, 'null'::jsonb)`).Scan(&j.ID, &j.Kind, &j.Params)
	if errors.Is(err, pgx.ErrNoRows) || ctx.Err() != nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	h, ok := r.handlers[j.Kind]
	var runErr error
	if !ok {
		runErr = fmt.Errorf("unknown job kind %q", j.Kind)
	} else {
		runErr = safely(func() error { return h(ctx, j) })
	}
	// finish on a fresh context so a shutdown does not leave the row in "running"
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if runErr != nil {
		r.log.Error("job failed", "kind", j.Kind, "id", j.ID, "error", runErr)
		_, err = r.pool.Exec(fctx, `UPDATE jobs SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`, j.ID, runErr.Error())
	} else {
		_, err = r.pool.Exec(fctx, `UPDATE jobs SET status = 'done', progress = COALESCE(total, progress), finished_at = now() WHERE id = $1`, j.ID)
	}
	return true, err
}

func safely(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn()
}

// Enqueue adds a job to the queue and returns its id.
func (r *Runner) Enqueue(ctx context.Context, kind string, params any, createdBy *uuid.UUID) (uuid.UUID, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `INSERT INTO jobs (kind, params, created_by) VALUES ($1, $2, $3) RETURNING id`,
		kind, raw, createdBy).Scan(&id)
	return id, err
}

// SetProgress lets a handler report how far it is.
func (r *Runner) SetProgress(ctx context.Context, id uuid.UUID, progress, total int) error {
	_, err := r.pool.Exec(ctx, `UPDATE jobs SET progress = $2, total = $3 WHERE id = $1`, id, progress, total)
	return err
}

type Info struct {
	ID       uuid.UUID
	Kind     string
	Status   string
	Progress int
	Total    *int
	Error    string
	Created  time.Time
	Finished *time.Time
}

func (r *Runner) Get(ctx context.Context, id uuid.UUID) (Info, error) {
	var i Info
	err := r.pool.QueryRow(ctx, `SELECT id, kind, status, progress, total, COALESCE(error, ''), created_at, finished_at
		FROM jobs WHERE id = $1`, id).Scan(&i.ID, &i.Kind, &i.Status, &i.Progress, &i.Total, &i.Error, &i.Created, &i.Finished)
	if errors.Is(err, pgx.ErrNoRows) {
		return Info{}, ErrNotFound
	}
	return i, err
}

// Summary counts jobs by status (for health checks).
func (r *Runner) Summary(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// FailedSince counts jobs that failed within the given window.
func (r *Runner) FailedSince(ctx context.Context, d time.Duration) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status = 'failed' AND finished_at > now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(d.Seconds()))).Scan(&n)
	return n, err
}
