package comments

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("comment not found")
	ErrInvalid  = errors.New("comment body is invalid")
)

type memoryRepository struct {
	mu       sync.RWMutex
	comments map[string]Comment
}

func NewMemoryRepository() Repository {
	return &memoryRepository{comments: make(map[string]Comment)}
}

func (r *memoryRepository) List(videoID string, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Comment, 0, limit)
	for _, comment := range r.comments {
		if comment.VideoID == videoID {
			result = append(result, comment)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *memoryRepository) Create(userID, videoID, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 2000 {
		return Comment{}, ErrInvalid
	}
	comment := Comment{ID: uuid.NewString(), UserID: userID, VideoID: videoID, Body: body, CreatedAt: time.Now().UTC()}
	r.mu.Lock()
	r.comments[comment.ID] = comment
	r.mu.Unlock()
	return comment, nil
}

func (r *memoryRepository) Delete(userID, commentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	comment, ok := r.comments[commentID]
	if !ok {
		return ErrNotFound
	}
	if comment.UserID != userID {
		return ErrNotFound
	}
	delete(r.comments, commentID)
	return nil
}
