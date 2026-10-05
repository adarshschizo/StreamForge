package playlists

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/videos"
)

var (
	ErrNotFound = errors.New("playlist not found")
	ErrInvalid  = errors.New("playlist name is invalid")
)

type Playlist struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Name      string         `json:"name"`
	Videos    []videos.Video `json:"videos"`
	CreatedAt time.Time      `json:"created_at"`
}

type Repository interface {
	Create(userID, name string) (Playlist, error)
	List(userID string) ([]Playlist, error)
	Get(userID, id string) (Playlist, error)
	AddVideo(userID, playlistID, videoID string) error
	RemoveVideo(userID, playlistID, videoID string) error
}

type memoryRepository struct {
	mu        sync.RWMutex
	playlists map[string]Playlist
	videos    videos.Repository
}

func NewMemoryRepository(videoRepository videos.Repository) Repository {
	return &memoryRepository{playlists: make(map[string]Playlist), videos: videoRepository}
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return "", ErrInvalid
	}
	return name, nil
}

func (r *memoryRepository) Create(userID, name string) (Playlist, error) {
	name, err := validName(name)
	if err != nil {
		return Playlist{}, err
	}
	item := Playlist{ID: uuid.NewString(), UserID: userID, Name: name, Videos: []videos.Video{}, CreatedAt: time.Now().UTC()}
	r.mu.Lock()
	r.playlists[item.ID] = item
	r.mu.Unlock()
	return item, nil
}

func (r *memoryRepository) List(userID string) ([]Playlist, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Playlist, 0)
	for _, item := range r.playlists {
		if item.UserID == userID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *memoryRepository) Get(userID, id string) (Playlist, error) {
	r.mu.RLock()
	item, ok := r.playlists[id]
	r.mu.RUnlock()
	if !ok || item.UserID != userID {
		return Playlist{}, ErrNotFound
	}
	return item, nil
}

func (r *memoryRepository) AddVideo(userID, playlistID, videoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.playlists[playlistID]
	if !ok || item.UserID != userID {
		return ErrNotFound
	}
	video, err := r.videos.GetByID(videoID)
	if err != nil {
		return ErrNotFound
	}
	for _, existing := range item.Videos {
		if existing.ID == video.ID {
			return nil
		}
	}
	item.Videos = append(item.Videos, video)
	r.playlists[playlistID] = item
	return nil
}

func (r *memoryRepository) RemoveVideo(userID, playlistID, videoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.playlists[playlistID]
	if !ok || item.UserID != userID {
		return ErrNotFound
	}
	for i, video := range item.Videos {
		if video.ID == videoID {
			item.Videos = append(item.Videos[:i], item.Videos[i+1:]...)
			r.playlists[playlistID] = item
			return nil
		}
	}
	return ErrNotFound
}

type postgresRepository struct{ db *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) Repository { return &postgresRepository{db: db} }

func (r *postgresRepository) Create(userID, name string) (Playlist, error) {
	name, err := validName(name)
	if err != nil {
		return Playlist{}, err
	}
	var item Playlist
	err = r.db.QueryRow(context.Background(), `INSERT INTO playlists (user_id, name) VALUES ($1,$2) RETURNING id,user_id,name,created_at`, userID, name).Scan(&item.ID, &item.UserID, &item.Name, &item.CreatedAt)
	item.Videos = []videos.Video{}
	return item, err
}

func (r *postgresRepository) List(userID string) ([]Playlist, error) {
	rows, err := r.db.Query(context.Background(), `SELECT id,user_id,name,created_at FROM playlists WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Playlist{}
	for rows.Next() {
		var item Playlist
		if err := rows.Scan(&item.ID, &item.UserID, &item.Name, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Videos = []videos.Video{}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *postgresRepository) Get(userID, id string) (Playlist, error) {
	var item Playlist
	err := r.db.QueryRow(context.Background(), `SELECT id,user_id,name,created_at FROM playlists WHERE id=$1 AND user_id=$2`, id, userID).Scan(&item.ID, &item.UserID, &item.Name, &item.CreatedAt)
	if err != nil {
		return Playlist{}, ErrNotFound
	}
	item.Videos = []videos.Video{}
	rows, err := r.db.Query(context.Background(), `SELECT v.id,v.user_id,v.title,v.description,v.status,v.visibility,v.original_key,v.duration_seconds,v.width,v.height,v.file_size,v.created_at,v.updated_at FROM playlist_videos pv JOIN videos v ON v.id=pv.video_id WHERE pv.playlist_id=$1 ORDER BY pv.position`, id)
	if err != nil {
		return Playlist{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var video videos.Video
		if err := rows.Scan(&video.ID, &video.UserID, &video.Title, &video.Description, &video.Status, &video.Visibility, &video.OriginalKey, &video.DurationSeconds, &video.Width, &video.Height, &video.FileSize, &video.CreatedAt, &video.UpdatedAt); err != nil {
			return Playlist{}, err
		}
		item.Videos = append(item.Videos, video)
	}
	return item, rows.Err()
}

func (r *postgresRepository) AddVideo(userID, playlistID, videoID string) error {
	var position int
	err := r.db.QueryRow(context.Background(), `SELECT count(*) FROM playlist_videos pv JOIN playlists p ON p.id=pv.playlist_id WHERE p.id=$1 AND p.user_id=$2`, playlistID, userID).Scan(&position)
	if err != nil {
		return ErrNotFound
	}
	_, err = r.db.Exec(context.Background(), `INSERT INTO playlist_videos (playlist_id,video_id,position) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, playlistID, videoID, position)
	return err
}

func (r *postgresRepository) RemoveVideo(userID, playlistID, videoID string) error {
	result, err := r.db.Exec(context.Background(), `DELETE FROM playlist_videos pv USING playlists p WHERE pv.playlist_id=p.id AND p.id=$1 AND p.user_id=$2 AND pv.video_id=$3`, playlistID, userID, videoID)
	if err != nil || result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
