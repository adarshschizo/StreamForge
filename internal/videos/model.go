package videos

import (
	"errors"
	"time"
)

const (
	StatusUploading  = "UPLOADING"
	StatusUploaded   = "UPLOADED"
	StatusProcessing = "PROCESSING"
	StatusReady      = "READY"
	StatusFailed     = "FAILED"

	VisibilityPublic   = "PUBLIC"
	VisibilityUnlisted = "UNLISTED"
	VisibilityPrivate  = "PRIVATE"
)

var (
	ErrNotFound     = errors.New("video not found")
	ErrNotOwner     = errors.New("video does not belong to user")
	ErrInvalidVideo = errors.New("invalid video")
)

type Video struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	Visibility      string    `json:"visibility"`
	OriginalKey     string    `json:"original_key,omitempty"`
	DurationSeconds *float64  `json:"duration_seconds,omitempty"`
	Width           *int      `json:"width,omitempty"`
	Height          *int      `json:"height,omitempty"`
	FileSize        *int64    `json:"file_size,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type UpdateInput struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

type Repository interface {
	Create(userID string, input CreateInput) (Video, error)
	ListByUser(userID string) []Video
	Search(query string, limit int) []Video
	GetForUser(userID, videoID string) (Video, error)
	GetByID(videoID string) (Video, error)
	UpdateForUser(userID, videoID string, input UpdateInput) (Video, error)
	DeleteForUser(userID, videoID string) error
	MarkUploaded(userID, videoID, originalKey string, fileSize int64) (Video, error)
}
