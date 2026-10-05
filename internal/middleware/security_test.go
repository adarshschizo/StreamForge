package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterRejectsAfterLimit(t *testing.T) {
	handler := NewRateLimiter(1, time.Minute).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "192.0.2.10:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, first)
	if response.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d", response.Code)
	}
	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "192.0.2.10:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, second)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", response.Code)
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing content type security header")
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing content security policy")
	}
}
