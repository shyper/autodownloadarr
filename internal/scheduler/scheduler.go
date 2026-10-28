// Package scheduler runs background work.
//
// Persistent tasks (Schedule) are stored in the database, survive restarts and
// are retried with backoff when their handler fails. Use them for anything that
// must eventually happen, such as deletion deadlines or searches. Handlers must
// be idempotent: after a crash a task may run again.
//
// Recurring jobs (Every) are in-memory housekeeping that simply runs again on
// the next tick.
package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"
)

// DefaultMaxAttempts is used when a Spec does not set MaxAttempts.
const DefaultMaxAttempts = 5

// ErrNoHandler is recorded for tasks whose type has no registered handler.
var ErrNoHandler = errors.New("no handler registered for task type")

// Task is a claimed task passed to a Handler.
type Task struct {
	ID          int64
	Type        string
	Key         string
	Payload     json.RawMessage
	RunAt       time.Time
	Attempt     int // 1 for the first run
	MaxAttempts int

	version int64
}

// Handler executes a task. Returning an error schedules a retry, unless the
// error is wrapped with Permanent or the task is out of attempts.
type Handler func(ctx context.Context, t Task) error

// Spec describes a task to schedule.
type Spec struct {
	Type string
	// Key optionally identifies the task. Scheduling again with the same key
	// replaces the existing task instead of adding a second one.
	Key         string
	Payload     any       // marshalled as JSON
	RunAt       time.Time // zero means now
	MaxAttempts int       // zero means DefaultMaxAttempts
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks err as not worth retrying.
func Permanent(err error) error { return permanentError{err} }

// Scheduler executes persistent tasks and recurring jobs.
type Scheduler struct {
	db           *sql.DB
	log          *slog.Logger
	now          func() time.Time
	workers      int
	pollInterval time.Duration
	backoff      func(attempt int) time.Duration

	mu       sync.RWMutex
	handlers map[string]Handler
	jobs     []recurringJob

	wake chan struct{}
}

type recurringJob struct {
	name     string
	interval time.Duration
	fn       func(context.Context) error
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithWorkers sets how many tasks may run concurrently.
func WithWorkers(n int) Option { return func(s *Scheduler) { s.workers = max(n, 1) } }

// WithPollInterval sets the upper bound between database checks.
func WithPollInterval(d time.Duration) Option { return func(s *Scheduler) { s.pollInterval = d } }

// WithBackoff sets the delay before retry number attempt+1.
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(s *Scheduler) { s.backoff = f }
}

// New creates a scheduler. Register handlers and jobs before calling Run.
func New(db *sql.DB, log *slog.Logger, opts ...Option) *Scheduler {
	s := &Scheduler{
		db:           db,
		log:          log,
		now:          time.Now,
		workers:      4,
		pollInterval: 5 * time.Second,
		backoff:      DefaultBackoff,
		handlers:     make(map[string]Handler),
		wake:         make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// DefaultBackoff doubles from 30 seconds up to one hour, plus up to 10% jitter.
func DefaultBackoff(attempt int) time.Duration {
	d := min(30*time.Second<<min(max(attempt-1, 0), 7), time.Hour)
	return d + time.Duration(rand.Int64N(int64(d/10)+1))
}

// Handle registers the handler for a task type.
func (s *Scheduler) Handle(taskType string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[taskType] = h
}

// Every registers a recurring in-memory job. It runs once when Run starts and
// then every interval.
func (s *Scheduler) Every(name string, interval time.Duration, fn func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, recurringJob{name: name, interval: interval, fn: fn})
}

// Schedule stores a task and returns its ID.
func (s *Scheduler) Schedule(ctx context.Context, spec Spec) (int64, error) {
	if spec.Type == "" {
		return 0, errors.New("scheduler: task type is required")
	}
	payload := []byte("{}")
	if spec.Payload != nil {
		b, err := json.Marshal(spec.Payload)
		if err != nil {
			return 0, fmt.Errorf("scheduler: marshal payload: %w", err)
		}
		payload = b
	}
	now := s.now()
	runAt := spec.RunAt
	if runAt.IsZero() {
		runAt = now
	}
	maxAttempts := spec.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	var key any // NULL keys never conflict
	if spec.Key != "" {
		key = spec.Key
	}

	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_tasks (type, dedupe_key, payload, run_at, max_attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (dedupe_key) DO UPDATE SET
			type = excluded.type,
			payload = excluded.payload,
			run_at = excluded.run_at,
			max_attempts = excluded.max_attempts,
			status = 'pending',
			attempts = 0,
			last_error = '',
			version = version + 1,
			updated_at = excluded.updated_at
		RETURNING id`,
		spec.Type, key, string(payload), runAt.UnixMilli(), maxAttempts, now.UnixMilli(), now.UnixMilli(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("scheduler: schedule %s: %w", spec.Type, err)
	}
	s.notify()
	return id, nil
}

// Cancel removes the task with the given key, if any.
func (s *Scheduler) Cancel(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE dedupe_key = ?`, key); err != nil {
		return fmt.Errorf("scheduler: cancel %s: %w", key, err)
	}
	return nil
}

// Run processes tasks until ctx is cancelled, then waits for running handlers
// to return. Tasks interrupted by a previous crash are picked up again.
func (s *Scheduler) Run(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET status = 'pending', updated_at = ? WHERE status = 'running'`,
		s.now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("scheduler: recover interrupted tasks: %w", err)
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	s.mu.RLock()
	jobs := slices.Clone(s.jobs)
	s.mu.RUnlock()
	for _, j := range jobs {
		wg.Go(func() { s.runRecurring(ctx, j) })
	}

	sem := make(chan struct{}, s.workers)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-s.wake:
		}

		if free := s.workers - len(sem); free > 0 {
			tasks, err := s.claim(ctx, free)
			if err != nil && ctx.Err() == nil {
				s.log.Error("scheduler: claim tasks", "err", err)
			}
			for _, t := range tasks {
				sem <- struct{}{}
				wg.Go(func() {
					defer func() {
						<-sem
						s.notify()
					}()
					s.execute(ctx, t)
				})
			}
		}
		timer.Reset(s.nextWait(ctx, s.workers-len(sem)))
	}
}

// nextWait returns how long to sleep before checking for due tasks again.
func (s *Scheduler) nextWait(ctx context.Context, free int) time.Duration {
	if free <= 0 {
		return s.pollInterval // a finishing worker wakes us earlier
	}
	var next sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MIN(run_at) FROM scheduled_tasks WHERE status = 'pending'`,
	).Scan(&next); err != nil || !next.Valid {
		return s.pollInterval
	}
	wait := time.UnixMilli(next.Int64).Sub(s.now())
	return min(max(wait, 10*time.Millisecond), s.pollInterval)
}

func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) claim(ctx context.Context, limit int) ([]Task, error) {
	now := s.now().UnixMilli()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE scheduled_tasks SET status = 'running', attempts = attempts + 1, updated_at = ?
		WHERE id IN (
			SELECT id FROM scheduled_tasks
			WHERE status = 'pending' AND run_at <= ?
			ORDER BY run_at, id
			LIMIT ?
		)
		RETURNING id, type, COALESCE(dedupe_key, ''), payload, run_at, attempts, max_attempts, version`,
		now, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var payload string
		var runAt int64
		if err := rows.Scan(&t.ID, &t.Type, &t.Key, &payload, &runAt, &t.Attempt, &t.MaxAttempts, &t.version); err != nil {
			return nil, err
		}
		t.Payload = json.RawMessage(payload)
		t.RunAt = time.UnixMilli(runAt)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Scheduler) execute(ctx context.Context, t Task) {
	s.mu.RLock()
	h, ok := s.handlers[t.Type]
	s.mu.RUnlock()

	var err error
	if ok {
		err = s.call(ctx, h, t)
	} else {
		err = Permanent(fmt.Errorf("%w: %s", ErrNoHandler, t.Type))
	}

	// Bookkeeping must happen even while shutting down.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := s.now().UnixMilli()
	log := s.log.With("task_id", t.ID, "type", t.Type, "attempt", t.Attempt)

	// Every update checks the version: if the task was rescheduled while it
	// ran, the new schedule wins and this result is discarded.
	var dbErr error
	var permanent permanentError
	switch {
	case err == nil:
		_, dbErr = s.db.ExecContext(bctx,
			`DELETE FROM scheduled_tasks WHERE id = ? AND version = ?`, t.ID, t.version)
	case ctx.Err() != nil:
		// Interrupted by shutdown: hand the task back without using up an attempt.
		_, dbErr = s.db.ExecContext(bctx,
			`UPDATE scheduled_tasks SET status = 'pending', attempts = attempts - 1, updated_at = ? WHERE id = ? AND version = ?`,
			now, t.ID, t.version)
	case errors.As(err, &permanent) || t.Attempt >= t.MaxAttempts:
		log.Error("scheduler: task failed permanently", "err", err)
		_, dbErr = s.db.ExecContext(bctx,
			`UPDATE scheduled_tasks SET status = 'failed', last_error = ?, updated_at = ? WHERE id = ? AND version = ?`,
			err.Error(), now, t.ID, t.version)
	default:
		retryAt := s.now().Add(s.backoff(t.Attempt))
		log.Warn("scheduler: task failed, will retry", "err", err, "retry_at", retryAt)
		_, dbErr = s.db.ExecContext(bctx,
			`UPDATE scheduled_tasks SET status = 'pending', run_at = ?, last_error = ?, updated_at = ? WHERE id = ? AND version = ?`,
			retryAt.UnixMilli(), err.Error(), now, t.ID, t.version)
	}
	if dbErr != nil {
		log.Error("scheduler: record task result", "err", dbErr)
	}
}

func (s *Scheduler) call(ctx context.Context, h Handler, t Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, t)
}

func (s *Scheduler) runRecurring(ctx context.Context, j recurringJob) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		if err := j.fn(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("scheduler: recurring job failed", "job", j.name, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
