package processing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/videos"
)

func newIntegrationStatusStore(t *testing.T) (*pgxpool.Pool, *PostgresStatusStore) {
	t.Helper()
	databaseURL := os.Getenv("STREAMFORGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set STREAMFORGE_TEST_DATABASE_URL to run PostgreSQL status-store integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to integration PostgreSQL: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping integration PostgreSQL: %v", err)
	}
	schema := "streamforge_test_" + uuid.NewString()[:8]
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated test schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		adminPool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema))
		adminPool.Close()
		t.Fatalf("parse integration PostgreSQL URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	testPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		adminPool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema))
		adminPool.Close()
		t.Fatalf("connect to isolated test schema: %v", err)
	}
	_, err = testPool.Exec(ctx, `
		CREATE TABLE videos (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err == nil {
		_, err = testPool.Exec(ctx, `
			CREATE TABLE processing_jobs (
				id TEXT PRIMARY KEY,
				video_id TEXT NOT NULL,
				status TEXT NOT NULL,
				attempts INTEGER NOT NULL DEFAULT 0,
				worker_id TEXT,
				error_message TEXT,
				started_at TIMESTAMPTZ,
				completed_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				progress INTEGER NOT NULL DEFAULT 0,
				stage TEXT NOT NULL DEFAULT 'queued'
			)
		`)
	}
	if err != nil {
		testPool.Close()
		adminPool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema))
		adminPool.Close()
		t.Fatalf("create isolated status-store tables: %v", err)
	}
	t.Cleanup(func() {
		testPool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
		adminPool.Close()
	})
	return testPool, NewPostgresStatusStore(testPool)
}

func TestPostgresStatusStoreClaimRetryAndRecovery(t *testing.T) {
	pool, store := newIntegrationStatusStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	job := queue.Job{ID: uuid.NewString(), VideoID: uuid.NewString()}
	insertPendingJob(t, ctx, pool, job)
	if err := store.Start(ctx, job, "worker-one"); err != nil {
		t.Fatalf("first Start() = %v, want success", err)
	}
	if err := store.Start(ctx, job, "worker-two"); !errors.Is(err, ErrDuplicateJob) {
		t.Fatalf("duplicate Start() = %v, want ErrDuplicateJob", err)
	}

	reclaimed := job
	reclaimed.Reclaimed = true
	if err := store.Start(ctx, reclaimed, "recovery-worker"); err != nil {
		t.Fatalf("reclaimed Start() = %v, want success", err)
	}
	var attempts int
	var workerID string
	if err := pool.QueryRow(ctx, `SELECT attempts, worker_id FROM processing_jobs WHERE id = $1`, job.ID).Scan(&attempts, &workerID); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || workerID != "recovery-worker" {
		t.Fatalf("recovered job state = attempts %d, worker %q; want attempts 2, recovery worker", attempts, workerID)
	}

	for attempt := 1; attempt <= queue.MaxAttempts; attempt++ {
		job.Attempts = attempt - 1
		if attempt > 1 {
			if err := store.Start(ctx, job, fmt.Sprintf("retry-worker-%d", attempt)); err != nil {
				t.Fatalf("Start() for failure attempt %d: %v", attempt, err)
			}
		}
		if err := store.Fail(ctx, job, errors.New("simulated processing failure")); err != nil {
			t.Fatalf("Fail() for attempt %d: %v", attempt, err)
		}
	}
	var status, videoStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM processing_jobs WHERE id = $1`, job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM videos WHERE id = $1`, job.VideoID).Scan(&videoStatus); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" || videoStatus != videos.StatusFailed {
		t.Fatalf("terminal statuses = job %q, video %q; want FAILED", status, videoStatus)
	}
}

func TestPostgresStatusStoreAllowsOneConcurrentClaim(t *testing.T) {
	pool, store := newIntegrationStatusStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	job := queue.Job{ID: uuid.NewString(), VideoID: uuid.NewString()}
	insertPendingJob(t, ctx, pool, job)

	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	for _, workerID := range []string{"worker-one", "worker-two"} {
		go func(workerID string) {
			defer wait.Done()
			results <- store.Start(ctx, job, workerID)
		}(workerID)
	}
	wait.Wait()
	close(results)

	successes, duplicates := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDuplicateJob):
			duplicates++
		default:
			t.Fatalf("concurrent Start() returned unexpected error: %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("concurrent claims = %d success, %d duplicate; want exactly one of each", successes, duplicates)
	}
}

func insertPendingJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, job queue.Job) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO videos (id, status) VALUES ($1, $2)`, job.VideoID, videos.StatusUploaded); err != nil {
		t.Fatalf("insert test video: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO processing_jobs (id, video_id, status) VALUES ($1, $2, 'PENDING')`, job.ID, job.VideoID); err != nil {
		t.Fatalf("insert test processing job: %v", err)
	}
}
