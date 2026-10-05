package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidInput       = errors.New("invalid input")
)

type Service struct {
	repository Repository
	jwtSecret  []byte
	tokenTTL   time.Duration
}

func NewService(repository Repository, jwtSecret string, tokenTTL time.Duration) (*Service, error) {
	if repository == nil || len(jwtSecret) < 32 {
		return nil, errors.New("authentication requires a repository and a JWT secret of at least 32 characters")
	}
	if tokenTTL <= 0 {
		return nil, errors.New("authentication token TTL must be positive")
	}
	return &Service{repository: repository, jwtSecret: []byte(jwtSecret), tokenTTL: tokenTTL}, nil
}

func (s *Service) Register(email, username, password string) (User, string, error) {
	if !validEmail(email) || len(username) < 3 || len(username) > 32 || len(password) < 8 {
		return User{}, "", ErrInvalidInput
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, "", fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repository.Create(email, username, hash)
	if err != nil {
		return User{}, "", err
	}
	token, err := s.issueToken(user)
	if err != nil {
		return User{}, "", fmt.Errorf("issue registration token: %w", err)
	}
	return user, token, nil
}

func (s *Service) Login(login, password string) (User, string, error) {
	user, err := s.repository.FindByLogin(login)
	if err != nil || !VerifyPassword(user.PasswordHash, password) {
		return User{}, "", ErrInvalidCredentials
	}
	token, err := s.issueToken(user)
	if err != nil {
		return User{}, "", fmt.Errorf("issue login token: %w", err)
	}
	return user, token, nil
}

func (s *Service) Authenticate(tokenString string) (User, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}

		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return User{}, ErrInvalidCredentials
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	subject, ok := claims["sub"].(string)
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return s.repository.FindByID(subject)
}

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", ErrInvalidInput
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	encode := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=1,p=4$%s$%s", encode(salt), encode(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=1,p=4" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func (s *Service) AuthenticateTokenFromRequest(r *http.Request) (User, error) {
	cookie, err := r.Cookie("streamforge_session")
	if err != nil {
		return User{}, ErrInvalidCredentials
	}
	return s.Authenticate(cookie.Value)
}

func (s *Service) issueToken(user User) (string, error) {
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID,
		"iat": now.Unix(),
		"exp": now.Add(s.tokenTTL).Unix(),
	}).SignedString(s.jwtSecret)
}

func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	return at > 0 && at < len(email)-1 && strings.Contains(email[at+1:], ".")
}
