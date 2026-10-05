package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateWindow struct {
	start time.Time
	count int
}

type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]rateWindow
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{clients: make(map[string]rateWindow), limit: limit, window: window}
}

func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		key := clientIP(r)
		now := time.Now()
		l.mu.Lock()
		state := l.clients[key]
		if state.start.IsZero() || now.Sub(state.start) >= l.window {
			state = rateWindow{start: now}
		}
		state.count++
		l.clients[key] = state
		allowed := state.count <= l.limit
		l.mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' http://localhost:8080 http://127.0.0.1:8080; media-src 'self' blob: https:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
