package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/jobs"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Zeitüberschreitung: %s", what)
}

func TestPeriodicTasksRunAndReportFailures(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := jobs.New(pool, 20*time.Millisecond)
	var okRuns, failRuns atomic.Int32
	r.AddPeriodic(jobs.Periodic{Name: "ok", Every: 20 * time.Millisecond, Run: func(context.Context) error { okRuns.Add(1); return nil }})
	r.AddPeriodic(jobs.Periodic{Name: "fail", Every: time.Hour, Run: func(context.Context) error { failRuns.Add(1); return errors.New("kaputt") }})
	r.AddPeriodic(jobs.Periodic{Name: "panic", Every: time.Hour, Run: func(context.Context) error { panic("boom") }})

	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mehrere Läufe", func() bool { return okRuns.Load() >= 3 })
	cancel()
	r.Wait()

	if failRuns.Load() != 1 {
		t.Errorf("fail lief %d-mal (Intervall 1h: nur beim Start)", failRuns.Load())
	}
	byName := map[string]jobs.PeriodicStatus{}
	for _, st := range r.PeriodicStatus() {
		byName[st.Name] = st
	}
	if byName["ok"].LastRun.IsZero() || byName["ok"].LastErr != "" {
		t.Errorf("ok: %+v", byName["ok"])
	}
	if byName["fail"].LastErr != "kaputt" {
		t.Errorf("fail: %+v", byName["fail"])
	}
	if byName["panic"].LastErr == "" {
		t.Errorf("Panic nicht abgefangen: %+v", byName["panic"])
	}
}

func TestQueuedJobs(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := jobs.New(pool, 20*time.Millisecond)
	var got atomic.Value
	r.Handle("echo", func(_ context.Context, j jobs.Job) error { got.Store(string(j.Params)); return nil })
	r.Handle("fail", func(context.Context, jobs.Job) error { return errors.New("geht nicht") })
	r.Handle("panic", func(context.Context, jobs.Job) error { panic("boom") })

	ctx := context.Background()
	okID, err := r.Enqueue(ctx, "echo", map[string]string{"a": "b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	failID, _ := r.Enqueue(ctx, "fail", nil, nil)
	panicID, _ := r.Enqueue(ctx, "panic", nil, nil)
	unknownID, _ := r.Enqueue(ctx, "gibtsnicht", nil, nil)

	runCtx, cancel := context.WithCancel(ctx)
	if err := r.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alle Jobs abgeschlossen", func() bool {
		s, _ := r.Summary(ctx)
		return s["queued"] == 0 && s["running"] == 0
	})
	cancel()
	r.Wait()

	check := func(id uuid.UUID, want, errPart string) {
		t.Helper()
		i, err := r.Get(ctx, id)
		if err != nil || i.Status != want || !strings.Contains(i.Error, errPart) || i.Finished == nil {
			t.Errorf("%v: %+v %v", id, i, err)
		}
	}
	check(okID, "done", "")
	check(failID, "failed", "geht nicht")
	check(panicID, "failed", "panic")
	check(unknownID, "failed", "unbekannte")
	var params map[string]string // jsonb normalizes spacing, so compare decoded
	if raw, _ := got.Load().(string); json.Unmarshal([]byte(raw), &params) != nil || params["a"] != "b" {
		t.Errorf("Parameter: %v", got.Load())
	}
	if _, err := r.Get(ctx, uuid.New()); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("unbekannte ID: %v", err)
	}
}

func TestEachJobRunsOnceWithTwoWorkers(t *testing.T) {
	pool := dbtest.NewPool(t)
	var runs atomic.Int32
	mk := func() *jobs.Runner {
		r := jobs.New(pool, 10*time.Millisecond)
		r.Handle("count", func(context.Context, jobs.Job) error { runs.Add(1); time.Sleep(5 * time.Millisecond); return nil })
		return r
	}
	a, b := mk(), mk()
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 20; i++ {
		if _, err := a.Enqueue(ctx, "count", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alle Jobs", func() bool { s, _ := a.Summary(ctx); return s["done"] == 20 })
	cancel()
	a.Wait()
	b.Wait()
	if runs.Load() != 20 {
		t.Errorf("Handler lief %d-mal für 20 Jobs", runs.Load())
	}
}

func TestStartMarksInterruptedJobsFailed(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs (kind, status, started_at) VALUES ('x', 'running', now())`); err != nil {
		t.Fatal(err)
	}
	r := jobs.New(pool, time.Hour)
	runCtx, cancel := context.WithCancel(ctx)
	if err := r.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	r.Wait()
	s, _ := r.Summary(ctx)
	if s["running"] != 0 || s["failed"] != 1 {
		t.Errorf("Status: %v", s)
	}
}
