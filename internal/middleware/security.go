package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
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

type RedisRateLimiter struct {
	client   *redis.Client
	scope    string
	limit    int
	window   time.Duration
	fallback *RateLimiter
}

func NewRedisRateLimiter(redisURL string, limit int, window time.Duration) (*RedisRateLimiter, error) {
	return NewRedisRateLimiterWithScope(redisURL, "global", limit, window)
}

func NewRedisRateLimiterWithScope(redisURL, scope string, limit int, window time.Duration) (*RedisRateLimiter, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	client := redis.NewClient(options)
	return &RedisRateLimiter{client: client, scope: scope, limit: limit, window: window, fallback: NewRateLimiter(limit, window)}, nil
}

func (l *RedisRateLimiter) Middleware(next http.Handler) http.Handler {
	return l.MiddlewareExceptPaths(next)
}

func (l *RedisRateLimiter) MiddlewareExceptPaths(next http.Handler, excludedPaths ...string) http.Handler {
	excluded := pathSet(excludedPaths)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isPathExcluded(excluded, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := fmt.Sprintf("streamforge:ratelimit:%s:%s", l.scope, clientIP(r))
		ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
		count, err := l.client.Incr(ctx, key).Result()
		if err == nil && count == 1 {
			err = l.client.Expire(ctx, key, l.window).Err()
		}
		cancel()
		if err != nil {
			l.fallback.MiddlewareExceptPaths(next, excludedPaths...).ServeHTTP(w, r)
			return
		}
		if count > int64(l.limit) {
			retryAfter := int64(l.window.Seconds())
			if ttl, ttlErr := l.client.TTL(context.Background(), key).Result(); ttlErr == nil && ttl > 0 {
				retryAfter = int64(ttl.Seconds()) + 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *RedisRateLimiter) Close() error {
	return l.client.Close()
}

func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return l.MiddlewareExceptPaths(next)
}

func (l *RateLimiter) MiddlewareExceptPaths(next http.Handler, excludedPaths ...string) http.Handler {
	excluded := pathSet(excludedPaths)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isPathExcluded(excluded, r.URL.Path) {
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

func pathSet(paths []string) map[string]struct{} {
	result := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		result[path] = struct{}{}
	}
	return result
}

func isPathExcluded(excluded map[string]struct{}, path string) bool {
	_, ok := excluded[path]
	return ok
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
