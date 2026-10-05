package videos

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Create(userID string, input CreateInput) (Video, error) {
	title := strings.TrimSpace(input.Title)
	visibility := normalizeVisibility(input.Visibility)
	if visibility == "" {
		visibility = VisibilityPrivate
	}
	if title == "" || len(title) > 200 || !validVisibility(visibility) {
		return Video{}, ErrInvalidVideo
	}
	video, err := scanVideo(r.db.QueryRow(context.Background(), `
		INSERT INTO videos (user_id, title, description, status, visibility)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
	`, userID, title, strings.TrimSpace(input.Description), StatusUploading, visibility))
	if err != nil {
		return Video{}, err
	}
	return video, nil
}

func (r *PostgresRepository) ListByUser(userID string) []Video {
	rows, err := r.db.Query(context.Background(), `
		SELECT id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
		FROM videos WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return []Video{}
	}

	defer rows.Close()
	result := make([]Video, 0)
	for rows.Next() {
		if video, scanErr := scanVideo(rows); scanErr == nil {
			result = append(result, video)
		}
	}
	return result
}

func (r *PostgresRepository) Search(query string, limit int) []Video {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	rows, err := r.db.Query(context.Background(), `
		SELECT id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
		FROM videos
		WHERE status = $1 AND visibility <> $2
			AND ($3 = '' OR title ILIKE '%' || $3 || '%' OR description ILIKE '%' || $3 || '%')
		ORDER BY created_at DESC LIMIT $4
	`, StatusReady, VisibilityPrivate, query, limit)
	if err != nil {
		return []Video{}
	}
	defer rows.Close()
	result := make([]Video, 0, limit)
	for rows.Next() {
		if video, scanErr := scanVideo(rows); scanErr == nil {
			result = append(result, video)
		}
	}
	return result
}

func (r *PostgresRepository) GetForUser(userID, videoID string) (Video, error) {
	video, err := scanVideo(r.db.QueryRow(context.Background(), `
		SELECT id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
		FROM videos WHERE id = $1
	`, videoID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrNotFound
	}

	if err != nil {
		return Video{}, err
	}

	if video.UserID != userID {
		return Video{}, ErrNotOwner
	}
	return video, nil
}

func (r *PostgresRepository) GetByID(videoID string) (Video, error) {
	video, err := scanVideo(r.db.QueryRow(context.Background(), `
		SELECT id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
		FROM videos WHERE id = $1
	`, videoID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrNotFound
	}
	if err != nil {
		return Video{}, err
	}
	return video, nil
}

func (r *PostgresRepository) UpdateForUser(userID, videoID string, input UpdateInput) (Video, error) {
	video, err := r.GetForUser(userID, videoID)
	if err != nil {
		return Video{}, err
	}
	if input.Title != nil {
		video.Title = strings.TrimSpace(*input.Title)
		if video.Title == "" || len(video.Title) > 200 {
			return Video{}, ErrInvalidVideo
		}
	}
	if input.Description != nil {
		video.Description = strings.TrimSpace(*input.Description)
	}
	if input.Visibility != nil {
		visibility := normalizeVisibility(*input.Visibility)
		if !validVisibility(visibility) {
			return Video{}, ErrInvalidVideo
		}
		video.Visibility = visibility
	}
	video, err = scanVideo(r.db.QueryRow(context.Background(), `
		UPDATE videos
		SET title = $1, description = $2, visibility = $3, updated_at = now()
		WHERE id = $4 AND user_id = $5
		RETURNING id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
	`, video.Title, video.Description, video.Visibility, videoID, userID))
	if err != nil {
		return Video{}, err
	}
	return video, nil
}

func (r *PostgresRepository) DeleteForUser(userID, videoID string) error {
	result, err := r.db.Exec(context.Background(), `DELETE FROM videos WHERE id = $1 AND user_id = $2`, videoID, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return r.ownerAwareNotFound(userID, videoID)
	}
	return nil
}

func (r *PostgresRepository) MarkUploaded(userID, videoID, originalKey string, fileSize int64) (Video, error) {
	video, err := scanVideo(r.db.QueryRow(context.Background(), `
		UPDATE videos
		SET status = $1, original_key = $2, file_size = $3, updated_at = now()
		WHERE id = $4 AND user_id = $5
		RETURNING id, user_id, title, description, status, visibility,
			original_key, duration_seconds, width, height, file_size, created_at, updated_at
	`, StatusUploaded, originalKey, fileSize, videoID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, r.ownerAwareNotFound(userID, videoID)
	}
	return video, err
}

func (r *PostgresRepository) ownerAwareNotFound(userID, videoID string) error {
	var exists bool
	if err := r.db.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM videos WHERE id = $1 AND user_id <> $2)`, videoID, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrNotOwner
	}
	return ErrNotFound
}

type videoRowScanner interface {
	Scan(...any) error
}

func scanVideo(row videoRowScanner) (Video, error) {
	var video Video
	var originalKey pgtype.Text
	var durationSeconds pgtype.Float8
	var width pgtype.Int4
	var height pgtype.Int4
	var fileSize pgtype.Int8
	err := row.Scan(
		&video.ID, &video.UserID, &video.Title, &video.Description, &video.Status,
		&video.Visibility, &originalKey, &durationSeconds, &width, &height,
		&fileSize, &video.CreatedAt, &video.UpdatedAt,
	)
	if err != nil {
		return Video{}, err
	}
	if originalKey.Valid {
		video.OriginalKey = originalKey.String
	}
	if durationSeconds.Valid {
		video.DurationSeconds = &durationSeconds.Float64
	}
	if width.Valid {
		value := int(width.Int32)
		video.Width = &value
	}
	if height.Valid {
		value := int(height.Int32)
		video.Height = &value
	}
	if fileSize.Valid {
		video.FileSize = &fileSize.Int64
	}
	return video, nil
}

var _ Repository = (*PostgresRepository)(nil)
