package auth

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrEmailAlreadyUsed    = errors.New("email already registered")
	ErrUsernameAlreadyUsed = errors.New("username already registered")
)

type MemoryRepository struct {
	mu      sync.RWMutex
	byID    map[string]User
	byEmail map[string]string
	byName  map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		byID:    make(map[string]User),
		byEmail: make(map[string]string),
		byName:  make(map[string]string),
	}
}

func (r *MemoryRepository) Create(email, username, passwordHash string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.TrimSpace(username)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byEmail[email]; exists {
		return User{}, ErrEmailAlreadyUsed
	}
	if _, exists := r.byName[strings.ToLower(username)]; exists {
		return User{}, ErrUsernameAlreadyUsed
	}

	user := User{
		ID:           uuid.NewString(),
		Email:        email,
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
	r.byID[user.ID] = user
	r.byEmail[email] = user.ID
	r.byName[strings.ToLower(username)] = user.ID
	return user, nil
}

func (r *MemoryRepository) FindByLogin(login string) (User, error) {
	login = strings.TrimSpace(login)
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.byEmail[strings.ToLower(login)]
	if !ok {
		id, ok = r.byName[strings.ToLower(login)]
	}
	if !ok {
		return User{}, ErrUserNotFound
	}
	return r.byID[id], nil
}

func (r *MemoryRepository) FindByID(id string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, ok := r.byID[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}
