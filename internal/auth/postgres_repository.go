package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Create(email, username, passwordHash string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.TrimSpace(username)
	var user User
	err := r.db.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, username, password_hash, created_at
	`, email, username, passwordHash).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_key") {
			return User{}, ErrEmailAlreadyUsed
		}
		if strings.Contains(err.Error(), "users_username_key") {
			return User{}, ErrUsernameAlreadyUsed
		}
		return User{}, err
	}
	return user, nil
}

func (r *PostgresRepository) FindByLogin(login string) (User, error) {
	var user User
	err := r.db.QueryRow(context.Background(), `
		SELECT id, email, username, password_hash, created_at
		FROM users
		WHERE lower(email) = lower($1) OR lower(username) = lower($1)
	`, strings.TrimSpace(login)).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (r *PostgresRepository) FindByID(id string) (User, error) {
	var user User
	err := r.db.QueryRow(context.Background(), `
		SELECT id, email, username, password_hash, created_at
		FROM users
		WHERE id = $1
	`, id).Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}
