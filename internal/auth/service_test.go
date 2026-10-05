package auth

import (
	"testing"
	"time"
)

func TestRegisterLoginAndAuthenticate(t *testing.T) {
	repository := NewMemoryRepository()
	service, err := NewService(repository, "a-test-secret-that-is-at-least-32-chars", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	user, token, err := service.Register("person@example.com", "person", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash == "" || user.PasswordHash == "correct horse battery staple" {
		t.Fatal("password was not protected")
	}
	if !VerifyPassword(user.PasswordHash, "correct horse battery staple") {
		t.Fatal("password should verify")
	}
	if VerifyPassword(user.PasswordHash, "wrong password") {
		t.Fatal("wrong password should not verify")
	}

	loggedIn, loginToken, err := service.Login("person", "correct horse battery staple")
	if err != nil || loggedIn.ID != user.ID {
		t.Fatalf("login failed: user=%+v err=%v", loggedIn, err)
	}
	authenticated, err := service.Authenticate(loginToken)
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("authentication failed: user=%+v err=%v", authenticated, err)
	}
	if token == "" {
		t.Fatal("registration token was empty")
	}
}
