package subscriptions

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("subscription not found")

type Subscription struct {
	CreatorID string    `json:"creator_id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository interface {
	Subscribe(subscriberID, creatorID string) error
	Unsubscribe(subscriberID, creatorID string) error
	List(subscriberID string) ([]Subscription, error)
}

type memoryRepository struct {
	mu      sync.RWMutex
	follows map[string]map[string]time.Time
	users   map[string]string
}

func NewMemoryRepository() Repository {
	return &memoryRepository{follows: make(map[string]map[string]time.Time), users: make(map[string]string)}
}

func (r *memoryRepository) Subscribe(subscriberID, creatorID string) error {
	if subscriberID == creatorID || creatorID == "" {
		return ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.follows[subscriberID] == nil {
		r.follows[subscriberID] = make(map[string]time.Time)
	}
	if _, ok := r.follows[subscriberID][creatorID]; !ok {
		r.follows[subscriberID][creatorID] = time.Now().UTC()
	}
	return nil
}

func (r *memoryRepository) Unsubscribe(subscriberID, creatorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.follows[subscriberID][creatorID]; !ok {
		return ErrNotFound
	}
	delete(r.follows[subscriberID], creatorID)
	return nil
}

func (r *memoryRepository) List(subscriberID string) ([]Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Subscription, 0, len(r.follows[subscriberID]))
	for creatorID, createdAt := range r.follows[subscriberID] {
		result = append(result, Subscription{CreatorID: creatorID, Username: r.users[creatorID], CreatedAt: createdAt})
	}
	return result, nil
}

type postgresRepository struct{ db *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) Repository { return &postgresRepository{db: db} }

func (r *postgresRepository) Subscribe(subscriberID, creatorID string) error {
	if subscriberID == creatorID {
		return ErrNotFound
	}
	_, err := r.db.Exec(context.Background(), `
		INSERT INTO subscriptions (subscriber_id, creator_id) VALUES ($1, $2)
		ON CONFLICT (subscriber_id, creator_id) DO NOTHING
	`, subscriberID, creatorID)
	return err
}

func (r *postgresRepository) Unsubscribe(subscriberID, creatorID string) error {
	result, err := r.db.Exec(context.Background(),
		`DELETE FROM subscriptions WHERE subscriber_id = $1 AND creator_id = $2`,
		subscriberID, creatorID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) List(subscriberID string) ([]Subscription, error) {
	rows, err := r.db.Query(context.Background(), `
		SELECT s.creator_id, u.username, s.created_at
		FROM subscriptions s JOIN users u ON u.id = s.creator_id
		WHERE s.subscriber_id = $1 ORDER BY s.created_at DESC
	`, subscriberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Subscription, 0)
	for rows.Next() {
		var item Subscription
		if err := rows.Scan(&item.CreatorID, &item.Username, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
