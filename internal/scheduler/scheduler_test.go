package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
)

func setup(t *testing.T) (*sql.DB, *Scheduler) {
	t.Helper()
	sqldb, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	s := New(sqldb, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithPollInterval(20*time.Millisecond),
		WithBackoff(func(int) time.Duration { return 0 }),
	)
	return sqldb, s
}

// start runs the scheduler until the test ends.
func start(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func countTasks(t *testing.T, sqldb *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM scheduled_tasks WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestTaskRunsAndIsRemoved(t *testing.T) {
	sqldb, s := setup(t)
	got := make(chan string, 1)
	s.Handle("greet", func(_ context.Context, task Task) error {
		got <- string(task.Payload)
		return nil
	})
	start(t, s)

	if _, err := s.Schedule(context.Background(), Spec{Type: "greet", Payload: map[string]string{"name": "plex"}}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	select {
	case p := <-got:
		if p != `{"name":"plex"}` {
			t.Errorf("payload = %s", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task did not run")
	}
	eventually(t, func() bool { return countTasks(t, sqldb, "1") == 0 })
}

func TestFutureTaskWaits(t *testing.T) {
	_, s := setup(t)
	var ran atomic.Bool
	s.Handle("later", func(context.Context, Task) error { ran.Store(true); return nil })
	start(t, s)

	if _, err := s.Schedule(context.Background(), Spec{Type: "later", RunAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if ran.Load() {
		t.Error("future task ran early")
	}
}

func TestRetriesThenFails(t *testing.T) {
	sqldb, s := setup(t)
	var calls atomic.Int32
	s.Handle("flaky", func(context.Context, Task) error {
		calls.Add(1)
		return errors.New("indexer unreachable")
	})
	start(t, s)

	id, err := s.Schedule(context.Background(), Spec{Type: "flaky", MaxAttempts: 3})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	eventually(t, func() bool { return countTasks(t, sqldb, "id = ? AND status = 'failed'", id) == 1 })
	if n := calls.Load(); n != 3 {
		t.Errorf("handler called %d times, want 3", n)
	}
	var lastErr string
	_ = sqldb.QueryRow(`SELECT last_error FROM scheduled_tasks WHERE id = ?`, id).Scan(&lastErr)
	if lastErr != "indexer unreachable" {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestPermanentErrorSkipsRetries(t *testing.T) {
	sqldb, s := setup(t)
	var calls atomic.Int32
	s.Handle("broken", func(context.Context, Task) error {
		calls.Add(1)
		return Permanent(errors.New("bad payload"))
	})
	start(t, s)

	id, _ := s.Schedule(context.Background(), Spec{Type: "broken"})
	eventually(t, func() bool { return countTasks(t, sqldb, "id = ? AND status = 'failed'", id) == 1 })
	if n := calls.Load(); n != 1 {
		t.Errorf("handler called %d times, want 1", n)
	}
}

func TestPanicIsRetried(t *testing.T) {
	sqldb, s := setup(t)
	var calls atomic.Int32
	s.Handle("panicky", func(context.Context, Task) error {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return nil
	})
	start(t, s)

	_, _ = s.Schedule(context.Background(), Spec{Type: "panicky"})
	eventually(t, func() bool { return calls.Load() == 2 && countTasks(t, sqldb, "1") == 0 })
}

func TestUnknownTypeFails(t *testing.T) {
	sqldb, s := setup(t)
	start(t, s)
	id, _ := s.Schedule(context.Background(), Spec{Type: "nobody-handles-this"})
	eventually(t, func() bool { return countTasks(t, sqldb, "id = ? AND status = 'failed'", id) == 1 })
}

func TestKeyReplacesAndCancels(t *testing.T) {
	sqldb, s := setup(t)
	ctx := context.Background()
	later := time.Now().Add(time.Hour)

	first, _ := s.Schedule(ctx, Spec{Type: "delete-movie", Key: "movie:1", RunAt: later})
	second, _ := s.Schedule(ctx, Spec{Type: "delete-movie", Key: "movie:1", RunAt: later.Add(time.Hour)})
	if first != second {
		t.Errorf("same key produced two tasks: %d, %d", first, second)
	}
	var runAt int64
	_ = sqldb.QueryRow(`SELECT run_at FROM scheduled_tasks WHERE id = ?`, first).Scan(&runAt)
	if runAt != later.Add(time.Hour).UnixMilli() {
		t.Error("run_at not replaced")
	}

	_, _ = s.Schedule(ctx, Spec{Type: "delete-movie", RunAt: later}) // no key: independent task
	if n := countTasks(t, sqldb, "1"); n != 2 {
		t.Errorf("tasks = %d, want 2", n)
	}

	if err := s.Cancel(ctx, "movie:1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if n := countTasks(t, sqldb, "dedupe_key = 'movie:1'"); n != 0 {
		t.Error("task not cancelled")
	}
}

func TestInterruptedTasksAreRecovered(t *testing.T) {
	sqldb, s := setup(t)
	ctx := context.Background()
	id, _ := s.Schedule(ctx, Spec{Type: "resume"})
	// Simulate a crash while the task was running.
	if _, err := sqldb.Exec(`UPDATE scheduled_tasks SET status = 'running', attempts = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	ran := make(chan struct{})
	s.Handle("resume", func(context.Context, Task) error { close(ran); return nil })
	start(t, s)

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted task was not resumed")
	}
}

func TestRecurringJobRunsImmediately(t *testing.T) {
	_, s := setup(t)
	var runs atomic.Int32
	s.Every("tick", time.Hour, func(context.Context) error { runs.Add(1); return nil })
	start(t, s)
	eventually(t, func() bool { return runs.Load() == 1 })
}

func TestDefaultBackoff(t *testing.T) {
	cases := []struct {
		attempt  int
		min, max time.Duration
	}{
		{1, 30 * time.Second, 33 * time.Second},
		{2, time.Minute, 66 * time.Second},
		{20, time.Hour, 66 * time.Minute},
	}
	for _, c := range cases {
		if d := DefaultBackoff(c.attempt); d < c.min || d > c.max {
			t.Errorf("DefaultBackoff(%d) = %s, want between %s and %s", c.attempt, d, c.min, c.max)
		}
	}
}
