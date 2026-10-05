package likes

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Summary struct {
	Liked bool `json:"liked"`
	Count int  `json:"count"`
}

type Repository interface {
	Toggle(userID, videoID string) (Summary, error)
	Get(userID, videoID string) (Summary, error)
}

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Toggle(userID, videoID string) (Summary, error) {
	ctx := context.Background()
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM video_likes WHERE video_id = $1 AND user_id = $2)`, videoID, userID).Scan(&exists)
	if err != nil {
		return Summary{}, err
	}
	if exists {
		_, err = tx.Exec(ctx, `DELETE FROM video_likes WHERE video_id = $1 AND user_id = $2`, videoID, userID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO video_likes (video_id, user_id) VALUES ($1, $2)`, videoID, userID)
	}
	if err != nil {
		return Summary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Summary{}, err
	}
	return r.Get(userID, videoID)
}

func (r *PostgresRepository) Get(userID, videoID string) (Summary, error) {
	var summary Summary
	err := r.db.QueryRow(context.Background(), `
		SELECT EXISTS(
			SELECT 1 FROM video_likes WHERE video_id = $1 AND user_id = $2
		), (SELECT count(*) FROM video_likes WHERE video_id = $1)
	`, videoID, userID).Scan(&summary.Liked, &summary.Count)
	return summary, err
}
