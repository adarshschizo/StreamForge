package comments

import "time"

type Comment struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository interface {
	List(videoID string, limit int) ([]Comment, error)
	Create(userID, videoID, body string) (Comment, error)
	Delete(userID, commentID string) error
}
