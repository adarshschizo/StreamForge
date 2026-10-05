package history

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/videos"
)

var ErrNotFound = errors.New("history entry not found")

type Entry struct {
	Video     videos.Video `json:"video"`
	WatchedAt time.Time    `json:"watched_at"`
}

type Repository interface {
	Record(userID, videoID string) error
	List(userID string, limit int) ([]Entry, error)
	Clear(userID, videoID string) error
}

type memoryRepository struct {
	mu      sync.RWMutex
	entries map[string]Entry
	videos  videos.Repository
}

func NewMemoryRepository(videoRepository videos.Repository) Repository {
	return &memoryRepository{entries: make(map[string]Entry), videos: videoRepository}
}

func (r *memoryRepository) Record(userID, videoID string) error {
	video, err := r.videos.GetByID(videoID)
	if err != nil {
		return ErrNotFound
	}
	r.mu.Lock()
	r.entries[userID+":"+videoID] = Entry{Video: video, WatchedAt: time.Now().UTC()}
	r.mu.Unlock()
	return nil
}

func (r *memoryRepository) List(userID string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	r.mu.RLock()
	result := make([]Entry, 0)
	for key, entry := range r.entries {
		if len(key) >= len(userID) && key[:len(userID)] == userID {
			result = append(result, entry)
		}
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].WatchedAt.After(result[j].WatchedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *memoryRepository) Clear(userID, videoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := userID + ":" + videoID
	if _, ok := r.entries[key]; !ok {
		return ErrNotFound
	}
	delete(r.entries, key)
	return nil
}

type postgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) Repository { return &postgresRepository{db: db} }

func (r *postgresRepository) Record(userID, videoID string) error {
	_, err := r.db.Exec(context.Background(), `
		INSERT INTO watch_history (user_id, video_id) VALUES ($1, $2)
		ON CONFLICT (user_id, video_id) DO UPDATE SET watched_at = now()
	`, userID, videoID)
	return err
}

func (r *postgresRepository) List(userID string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.Query(context.Background(), `
		SELECT v.id, v.user_id, v.title, v.description, v.status, v.visibility,
			v.original_key, v.duration_seconds, v.width, v.height, v.file_size,
			v.created_at, v.updated_at, h.watched_at
		FROM watch_history h JOIN videos v ON v.id = h.video_id
		WHERE h.user_id = $1 ORDER BY h.watched_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Entry, 0, limit)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Video.ID, &entry.Video.UserID, &entry.Video.Title,
			&entry.Video.Description, &entry.Video.Status, &entry.Video.Visibility,
			&entry.Video.OriginalKey, &entry.Video.DurationSeconds, &entry.Video.Width,
			&entry.Video.Height, &entry.Video.FileSize, &entry.Video.CreatedAt,
			&entry.Video.UpdatedAt, &entry.WatchedAt); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (r *postgresRepository) Clear(userID, videoID string) error {
	result, err := r.db.Exec(context.Background(), `DELETE FROM watch_history WHERE user_id = $1 AND video_id = $2`, userID, videoID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
