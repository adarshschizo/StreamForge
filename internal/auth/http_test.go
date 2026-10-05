package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthenticationFlow(t *testing.T) {
	service, err := NewService(NewMemoryRepository(), "a-test-secret-that-is-at-least-32-chars", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, false)

	register := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"person@example.com","username":"person","password":"correct horse battery staple"}`))
	register.Header.Set("Content-Type", "application/json")
	registerResponse := httptest.NewRecorder()
	handler.Register(registerResponse, register)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("expected register status 201, got %d: %s", registerResponse.Code, registerResponse.Body.String())
	}
	session := registerResponse.Result().Cookies()[0]
	if !session.HttpOnly || session.Name != "streamforge_session" {
		t.Fatalf("expected HTTP-only session cookie, got %+v", session)
	}
	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"login":"person","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.Login(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
}
