package auth

import "time"

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Repository interface {
	Create(email, username, passwordHash string) (User, error)
	FindByLogin(login string) (User, error)
	FindByID(id string) (User, error)
}
