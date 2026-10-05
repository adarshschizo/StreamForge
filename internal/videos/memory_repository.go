package videos

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryRepository struct {
	mu     sync.RWMutex
	videos map[string]Video
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{videos: make(map[string]Video)}
}

func (r *MemoryRepository) Create(userID string, input CreateInput) (Video, error) {
	title := strings.TrimSpace(input.Title)
	visibility := input.Visibility
	if visibility == "" {
		visibility = VisibilityPrivate
	}
	if title == "" || len(title) > 200 || !validVisibility(visibility) {
		return Video{}, ErrInvalidVideo
	}
	now := time.Now().UTC()
	video := Video{
		ID:          uuid.NewString(),
		UserID:      userID,
		Title:       title,
		Description: strings.TrimSpace(input.Description),
		Status:      StatusUploading,
		Visibility:  visibility,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	r.mu.Lock()
	r.videos[video.ID] = video
	r.mu.Unlock()
	return video, nil
}

func (r *MemoryRepository) ListByUser(userID string) []Video {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Video, 0)
	for _, video := range r.videos {
		if video.UserID == userID {
			result = append(result, video)
		}
	}
	return result
}

func (r *MemoryRepository) Search(query string, limit int) []Video {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Video, 0, limit)
	for _, video := range r.videos {
		if video.Status != StatusReady || video.Visibility == VisibilityPrivate {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(video.Title), query) && !strings.Contains(strings.ToLower(video.Description), query) {
			continue
		}
		result = append(result, video)
		if len(result) == limit {
			break
		}
	}
	return result
}

func (r *MemoryRepository) GetForUser(userID, videoID string) (Video, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	video, ok := r.videos[videoID]
	if !ok {
		return Video{}, ErrNotFound
	}

	if video.UserID != userID {
		return Video{}, ErrNotOwner
	}

	return video, nil
}

func (r *MemoryRepository) GetByID(videoID string) (Video, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	video, ok := r.videos[videoID]
	if !ok {
		return Video{}, ErrNotFound
	}
	return video, nil
}

func (r *MemoryRepository) UpdateForUser(userID, videoID string, input UpdateInput) (Video, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	video, ok := r.videos[videoID]
	if !ok {
		return Video{}, ErrNotFound
	}
	if video.UserID != userID {
		return Video{}, ErrNotOwner
	}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" || len(title) > 200 {
			return Video{}, ErrInvalidVideo
		}
		video.Title = title
	}
	if input.Description != nil {
		video.Description = strings.TrimSpace(*input.Description)
	}
	if input.Visibility != nil {
		if !validVisibility(*input.Visibility) {
			return Video{}, ErrInvalidVideo
		}
		video.Visibility = *input.Visibility
	}
	video.UpdatedAt = time.Now().UTC()
	r.videos[video.ID] = video
	return video, nil
}

func (r *MemoryRepository) DeleteForUser(userID, videoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	video, ok := r.videos[videoID]
	if !ok {
		return ErrNotFound
	}
	if video.UserID != userID {
		return ErrNotOwner
	}
	delete(r.videos, videoID)
	return nil
}

func (r *MemoryRepository) MarkUploaded(userID, videoID, originalKey string, fileSize int64) (Video, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	video, ok := r.videos[videoID]
	if !ok {
		return Video{}, ErrNotFound
	}
	if video.UserID != userID {
		return Video{}, ErrNotOwner
	}
	video.Status = StatusUploaded
	video.OriginalKey = originalKey
	video.FileSize = &fileSize
	video.UpdatedAt = time.Now().UTC()
	r.videos[video.ID] = video
	return video, nil
}

func validVisibility(value string) bool {
	return value == VisibilityPublic || value == VisibilityUnlisted || value == VisibilityPrivate
}
