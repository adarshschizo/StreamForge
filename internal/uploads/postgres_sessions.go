package uploads

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresMultipartSessionStore struct {
	db *pgxpool.Pool
}

func NewMultipartSessionStore(db *pgxpool.Pool) *PostgresMultipartSessionStore {
	return &PostgresMultipartSessionStore{db: db}
}

func (s *PostgresMultipartSessionStore) Create(ctx context.Context, videoID, key, uploadID string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO upload_sessions (video_id, storage_key, upload_id, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (upload_id) DO UPDATE SET status = 'ACTIVE', expires_at = EXCLUDED.expires_at
	`, videoID, key, uploadID, expiresAt)
	return err
}

func (s *PostgresMultipartSessionStore) Part(ctx context.Context, uploadID string, partNumber int, size int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO upload_parts (upload_id, part_number, size_bytes)
		VALUES ($1, $2, $3)
		ON CONFLICT (upload_id, part_number) DO UPDATE SET size_bytes = EXCLUDED.size_bytes, uploaded_at = now()
	`, uploadID, partNumber, size)
	return err
}

func (s *PostgresMultipartSessionStore) Complete(ctx context.Context, uploadID string) error {
	_, err := s.db.Exec(ctx, `UPDATE upload_sessions SET status = 'COMPLETED' WHERE upload_id = $1`, uploadID)
	return err
}

func (s *PostgresMultipartSessionStore) Abort(ctx context.Context, uploadID string) error {
	_, err := s.db.Exec(ctx, `UPDATE upload_sessions SET status = 'ABORTED' WHERE upload_id = $1`, uploadID)
	return err
}
