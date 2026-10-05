package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yourusername/streamforge/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	handler := newRouter(config.Config{Version: "test", Environment: "test", JWTSecret: "a-test-secret-that-is-at-least-32-chars"}, slog.Default())
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if got := response.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected response: %s", got)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	handler := newRouter(config.Config{JWTSecret: "a-test-secret-that-is-at-least-32-chars"}, slog.Default())
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", response.Code)
	}
}
