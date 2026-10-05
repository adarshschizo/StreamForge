package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRedisRateLimiterRejectsInvalidURL(t *testing.T) {
	if _, err := NewRedisRateLimiter("://invalid", 1, time.Minute); err == nil {
		t.Fatal("expected invalid Redis URL to fail")
	}
}

func TestRateLimiterAllowsOptions(t *testing.T) {
	handler := NewRateLimiter(1, time.Minute).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected OPTIONS to bypass limiter, got %d", recorder.Code)
	}
}
