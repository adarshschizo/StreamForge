package comments

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct{ db *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) Repository {
	return &postgresRepository{db: db}
}

func (r *postgresRepository) List(videoID string, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.Query(context.Background(), `
		SELECT c.id, c.video_id, c.user_id, u.username, c.body, c.created_at
		FROM video_comments c JOIN users u ON u.id = c.user_id
		WHERE c.video_id = $1 ORDER BY c.created_at DESC LIMIT $2
	`, videoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Comment, 0, limit)
	for rows.Next() {
		var comment Comment
		if err := rows.Scan(&comment.ID, &comment.VideoID, &comment.UserID, &comment.Username, &comment.Body, &comment.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, comment)
	}
	return result, rows.Err()
}

func (r *postgresRepository) Create(userID, videoID, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 2000 {
		return Comment{}, ErrInvalid
	}
	var comment Comment
	err := r.db.QueryRow(context.Background(), `
		INSERT INTO video_comments (video_id, user_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, video_id, user_id, body, created_at
	`, videoID, userID, body).Scan(&comment.ID, &comment.VideoID, &comment.UserID, &comment.Body, &comment.CreatedAt)
	comment.Username = ""
	return comment, err
}

func (r *postgresRepository) Delete(userID, commentID string) error {
	result, err := r.db.Exec(context.Background(), `DELETE FROM video_comments WHERE id = $1 AND user_id = $2`, commentID, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
