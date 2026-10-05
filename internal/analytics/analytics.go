package analytics

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/videos"
)

type Summary struct {
	VideoID string    `json:"video_id"`
	Title   string    `json:"title"`
	Views   int64     `json:"views"`
	Updated time.Time `json:"updated_at"`
}

type Repository interface {
	Record(videoID, viewerID string) error
	OwnerSummary(ownerID string) ([]Summary, error)
}

type memoryRepository struct {
	mu     sync.RWMutex
	views  map[string]int64
	videos videos.Repository
}

func NewMemoryRepository(videosRepository videos.Repository) Repository {
	return &memoryRepository{views: make(map[string]int64), videos: videosRepository}
}

func (r *memoryRepository) Record(videoID, _ string) error {
	r.mu.Lock()
	r.views[videoID]++
	r.mu.Unlock()
	return nil
}

func (r *memoryRepository) OwnerSummary(ownerID string) ([]Summary, error) {
	result := make([]Summary, 0)
	for _, video := range r.videos.ListByUser(ownerID) {
		r.mu.RLock()
		count := r.views[video.ID]
		r.mu.RUnlock()
		result = append(result, Summary{VideoID: video.ID, Title: video.Title, Views: count, Updated: video.UpdatedAt})
	}
	return result, nil
}

type postgresRepository struct{ db *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) Repository { return &postgresRepository{db: db} }

func (r *postgresRepository) Record(videoID, viewerID string) error {
	var value any
	if viewerID != "" {
		value = viewerID
	}
	_, err := r.db.Exec(context.Background(), `INSERT INTO video_views (video_id, viewer_id) VALUES ($1, $2)`, videoID, value)
	return err
}

func (r *postgresRepository) OwnerSummary(ownerID string) ([]Summary, error) {
	rows, err := r.db.Query(context.Background(), `
		SELECT v.id, v.title, count(vw.id), max(vw.viewed_at)
		FROM videos v LEFT JOIN video_views vw ON vw.video_id = v.id
		WHERE v.user_id = $1 GROUP BY v.id, v.title ORDER BY count(vw.id) DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Summary, 0)
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.VideoID, &item.Title, &item.Views, &item.Updated); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
