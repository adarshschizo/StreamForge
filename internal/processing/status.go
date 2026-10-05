package processing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/videos"
)

type StatusStore interface {
	Create(context.Context, queue.Job) error
	Start(context.Context, queue.Job, string) error
	UpdateProgress(context.Context, queue.Job, int, string) error
	GetStatus(context.Context, string) (Status, error)
	Complete(context.Context, queue.Job, Result) error
	Fail(context.Context, queue.Job, error) error
}

var ErrDuplicateJob = errors.New("processing job already claimed or finished")

type NoopStatusStore struct{}

func (NoopStatusStore) Create(context.Context, queue.Job) error                      { return nil }
func (NoopStatusStore) Start(context.Context, queue.Job, string) error               { return nil }
func (NoopStatusStore) UpdateProgress(context.Context, queue.Job, int, string) error { return nil }
func (NoopStatusStore) GetStatus(context.Context, string) (Status, error) {
	return Status{}, videos.ErrNotFound
}
func (NoopStatusStore) Complete(context.Context, queue.Job, Result) error { return nil }
func (NoopStatusStore) Fail(context.Context, queue.Job, error) error      { return nil }

type PostgresStatusStore struct {
	db *pgxpool.Pool
}

func (s *PostgresStatusStore) GetStatus(ctx context.Context, videoID string) (Status, error) {
	var status Status
	err := s.db.QueryRow(ctx, `
		SELECT video_id, status, progress, stage, COALESCE(error_message, '')
		FROM processing_jobs WHERE video_id = $1 ORDER BY created_at DESC LIMIT 1
	`, videoID).Scan(&status.VideoID, &status.Status, &status.Progress, &status.Stage, &status.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, videos.ErrNotFound
	}
	return status, err
}

func NewPostgresStatusStore(db *pgxpool.Pool) *PostgresStatusStore {
	return &PostgresStatusStore{db: db}
}

func (s *PostgresStatusStore) Create(ctx context.Context, job queue.Job) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO processing_jobs (id, video_id, status, attempts)
		VALUES ($1, $2, 'PENDING', $3)
		ON CONFLICT (id) DO NOTHING
	`, job.ID, job.VideoID, job.Attempts)
	return err
}

func (s *PostgresStatusStore) Start(ctx context.Context, job queue.Job, workerID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin processing job claim: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE processing_jobs
		SET status = 'PROCESSING',
			attempts = CASE WHEN $4 THEN attempts + 1 ELSE $1 END,
			worker_id = $2,
			started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $3 AND (status = 'PENDING' OR ($4 AND status = 'PROCESSING'))
	`, job.Attempts+1, workerID, job.ID, job.Reclaimed)
	if err != nil {
		return fmt.Errorf("claim processing job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM processing_jobs WHERE id = $1`, job.ID).Scan(&status); err != nil {
			return fmt.Errorf("read unclaimed processing job status: %w", err)
		}
		if status == "PROCESSING" || status == "COMPLETED" || status == "FAILED" {
			return ErrDuplicateJob
		}
		return fmt.Errorf("processing job %q is not claimable from status %q", job.ID, status)
	}

	_, err = tx.Exec(ctx, `
		UPDATE videos SET status = $1, updated_at = now()
		WHERE id = $2 AND status IN ($3, $4, $5)
	`, videos.StatusProcessing, job.VideoID, videos.StatusUploaded, videos.StatusProcessing, videos.StatusFailed)
	if err != nil {
		return fmt.Errorf("set video processing status: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStatusStore) UpdateProgress(ctx context.Context, job queue.Job, progress int, stage string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := s.db.Exec(ctx, `
		UPDATE processing_jobs SET progress = $1, stage = $2, updated_at = now() WHERE id = $3
	`, progress, stage, job.ID)
	return err
}

func (s *PostgresStatusStore) Complete(ctx context.Context, job queue.Job, result Result) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE processing_jobs
		SET status = 'COMPLETED', progress = 100, stage = 'completed',
			completed_at = now(), updated_at = now(), error_message = NULL
		WHERE id = $1
	`, job.ID)
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE videos SET status = $1, duration_seconds = $2, width = $3, height = $4, updated_at = now()
		WHERE id = $5
	`, videos.StatusReady, result.DurationSeconds, result.Width, result.Height, job.VideoID)
	if err != nil {
		return err
	}
	for _, resolution := range result.Resolutions {
		_, err = tx.Exec(ctx, `
			INSERT INTO video_renditions (video_id, resolution, width, height, codec, playlist_key)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (video_id, resolution) DO UPDATE SET width = EXCLUDED.width,
				height = EXCLUDED.height, codec = EXCLUDED.codec, playlist_key = EXCLUDED.playlist_key
		`, job.VideoID, fmt.Sprintf("%dp", resolution), resolution*16/9, resolution, "h264/aac",
			fmt.Sprintf("videos/%s/hls/%dp/index.m3u8", job.VideoID, resolution))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStatusStore) Fail(ctx context.Context, job queue.Job, cause error) error {
	message := "processing failed"
	if cause != nil {
		message = cause.Error()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed processing job update: %w", err)
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `
		UPDATE processing_jobs
		SET status = CASE WHEN attempts >= $1 THEN 'FAILED' ELSE 'PENDING' END,
			progress = CASE WHEN attempts >= $1 THEN progress ELSE 0 END,
			stage = CASE WHEN attempts >= $1 THEN 'failed' ELSE 'queued' END,
			error_message = $2, updated_at = now(),
			completed_at = CASE WHEN attempts >= $1 THEN now() ELSE completed_at END
		WHERE id = $3
		RETURNING status
	`, queue.MaxAttempts, message, job.ID).Scan(&status)
	if err != nil {
		return fmt.Errorf("update failed processing job: %w", err)
	}
	if status == "FAILED" {
		_, err = tx.Exec(ctx, `UPDATE videos SET status = $1, updated_at = now() WHERE id = $2`,
			videos.StatusFailed, job.VideoID)
		if err != nil {
			return fmt.Errorf("mark video processing as failed: %w", err)
		}
	}
	return tx.Commit(ctx)
}

var _ StatusStore = (*PostgresStatusStore)(nil)
