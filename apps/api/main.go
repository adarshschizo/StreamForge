package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/analytics"
	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/comments"
	"github.com/yourusername/streamforge/internal/config"
	"github.com/yourusername/streamforge/internal/history"
	"github.com/yourusername/streamforge/internal/likes"
	"github.com/yourusername/streamforge/internal/metrics"
	"github.com/yourusername/streamforge/internal/middleware"
	"github.com/yourusername/streamforge/internal/playlists"
	"github.com/yourusername/streamforge/internal/processing"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/storage"
	"github.com/yourusername/streamforge/internal/streaming"
	"github.com/yourusername/streamforge/internal/subscriptions"
	"github.com/yourusername/streamforge/internal/telemetry"
	"github.com/yourusername/streamforge/internal/uploads"
	"github.com/yourusername/streamforge/internal/videos"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	shutdownTrace, err := telemetry.Init(logger, cfg.OTELServiceName+"-api", cfg.OTELExporterEndpoint, cfg.OTELEnabled)
	if err != nil {
		logger.Error("telemetry init failed", "error", err)
		os.Exit(1)
	}
	defer shutdownTrace()

	handler, cleanup := newRouterWithCleanup(cfg, logger)
	defer cleanup()

	server := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("api server listening", "addr", cfg.APIAddr, "version", cfg.Version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api server stopped unexpectedly", "error", err)
			stop()
		}
	}()

	<-shutdownCtx.Done()
	logger.Info("shutting down api server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("api shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("api server stopped")
}

func newRouter(cfg config.Config, logger *slog.Logger) http.Handler {
	handler, _ := newRouterWithCleanup(cfg, logger)
	return handler
}

func newRouterWithCleanup(cfg config.Config, logger *slog.Logger) (http.Handler, func()) {
	mux := http.NewServeMux()
	apiMetrics := metrics.New()
	var cleanup func()
	var authRepository auth.Repository = auth.NewMemoryRepository()
	var videoRepository videos.Repository = videos.NewMemoryRepository()
	var likeRepository likes.Repository = likes.NewMemoryRepositoryWithMutex()
	var commentRepository comments.Repository = comments.NewMemoryRepository()
	var historyRepository history.Repository
	var playlistRepository playlists.Repository = playlists.NewMemoryRepository(videoRepository)
	var subscriptionRepository subscriptions.Repository = subscriptions.NewMemoryRepository()
	var analyticsRepository analytics.Repository
	var jobStore uploads.JobStore
	var multipartSessions uploads.MultipartSessionStore
	var statusStore processing.StatusStore = processing.NoopStatusStore{}
	if cfg.DatabaseMode == "postgres" {
		pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
		if err != nil {
			logger.Error("database setup failed", "error", err)
			panic(err)
		}
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err != nil {
			pool.Close()
			logger.Error("database connection failed", "error", err)
			panic(err)
		}
		cleanup = pool.Close
		authRepository = auth.NewPostgresRepository(pool)
		videoRepository = videos.NewPostgresRepository(pool)
		likeRepository = likes.NewPostgresRepository(pool)
		commentRepository = comments.NewPostgresRepository(pool)
		historyRepository = history.NewPostgresRepository(pool)
		analyticsRepository = analytics.NewPostgresRepository(pool)
		playlistRepository = playlists.NewPostgresRepository(pool)
		subscriptionRepository = subscriptions.NewPostgresRepository(pool)
		jobStore = processing.NewPostgresStatusStore(pool)
		statusStore = jobStore.(processing.StatusStore)
		multipartSessions = uploads.NewMultipartSessionStore(pool)
	}
	if historyRepository == nil {
		historyRepository = history.NewMemoryRepository(videoRepository)
	}
	if analyticsRepository == nil {
		analyticsRepository = analytics.NewMemoryRepository(videoRepository)
	}
	authService, err := auth.NewService(authRepository, cfg.JWTSecret, 24*time.Hour)
	if err != nil {
		logger.Error("authentication setup failed", "error", err)
		panic(err)
	}
	authHandler := auth.NewHTTPHandler(authService, cfg.CookieSecure)
	videoHandler := videos.NewHTTPHandler(videoRepository)
	likeHandler := likes.NewHTTPHandler(likeRepository, videoRepository)
	commentHandler := comments.NewHTTPHandler(commentRepository, videoRepository)
	playlistHandler := playlists.NewHTTPHandler(playlistRepository)
	subscriptionHandler := subscriptions.NewHTTPHandler(subscriptionRepository)
	var storageService storage.Service = storage.NewMemoryService()
	var playbackStore storage.PlaybackStore
	addStorageCleanup := func(store *storage.S3Service, name string) {
		previousCleanup := cleanup
		cleanup = func() {
			if closeErr := store.Close(); closeErr != nil {
				logger.Error(name+" object storage cleanup failed", "error", closeErr)
			}
			if previousCleanup != nil {
				previousCleanup()
			}
		}
	}
	if cfg.StorageMode == "s3" {
		originalsStore, storeErr := storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.OriginalsBucket, cfg.StorageUseSSL)
		err = storeErr
		if err != nil {
			logger.Error("object storage setup failed", "error", err)
			panic(err)
		}
		addStorageCleanup(originalsStore, "original")
		storageService = originalsStore

		processedStore, storeErr := storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.ProcessedBucket, cfg.StorageUseSSL)
		err = storeErr
		if err != nil {
			logger.Error("processed storage setup failed", "error", err)
			panic(err)
		}
		addStorageCleanup(processedStore, "processed")
		playbackStore = processedStore
	}
	var publisher uploads.Publisher = queue.NewMemoryQueue()
	if cfg.QueueMode == "redis" {
		publisher, err = queue.NewRedis(cfg.RedisURL)
		if err != nil {
			logger.Error("queue setup failed", "error", err)
			panic(err)
		}
	}
	uploadHandler := uploads.NewHTTPHandler(videoRepository, storageService, publisher, jobStore)
	uploadHandler.SetMultipartSessionStore(multipartSessions)
	streamHandler := streaming.NewHTTPHandler(videoRepository, playbackStore, historyRepository)
	streamHandler.SetAnalytics(analyticsRepository)
	analyticsHandler := analytics.NewHTTPHandler(analyticsRepository)
	historyHandler := history.NewHTTPHandler(historyRepository)
	processingHandler := processing.NewHTTPHandler(videoRepository, statusStore)
	requireAuth := func(handler http.Handler) http.Handler {
		return middleware.RequireAuth(authService, handler)
	}

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": cfg.Version, "environment": cfg.Environment})
	})
	mux.Handle("GET /metrics", apiMetrics)
	mux.HandleFunc("POST /auth/register", authHandler.Register)
	loginRateLimiter := middleware.NewRateLimiter(10, time.Minute)
	var loginHandler http.Handler = loginRateLimiter.Middleware(http.HandlerFunc(authHandler.Login))
	if cfg.QueueMode == "redis" {
		if redisLoginLimiter, limiterErr := middleware.NewRedisRateLimiterWithScope(cfg.RedisURL, "login", 10, time.Minute); limiterErr == nil {
			loginHandler = redisLoginLimiter.Middleware(http.HandlerFunc(authHandler.Login))
		} else {
			logger.Warn("redis login rate limiter unavailable; using in-memory limiter", "error", limiterErr)
		}
	}
	mux.Handle("POST /auth/login", loginHandler)
	mux.HandleFunc("POST /auth/logout", authHandler.Logout)
	mux.Handle("GET /auth/me", middleware.RequireAuth(authService, http.HandlerFunc(authHandler.Me)))
	mux.Handle("POST /videos", requireAuth(http.HandlerFunc(videoHandler.Create)))
	mux.Handle("GET /videos", requireAuth(http.HandlerFunc(videoHandler.List)))
	mux.HandleFunc("GET /search/videos", videoHandler.Search)
	mux.Handle("GET /videos/{id}/likes", requireAuth(http.HandlerFunc(likeHandler.Get)))
	mux.Handle("POST /videos/{id}/like", requireAuth(http.HandlerFunc(likeHandler.Toggle)))
	mux.HandleFunc("GET /videos/{id}/comments", commentHandler.List)
	mux.Handle("POST /videos/{id}/comments", requireAuth(http.HandlerFunc(commentHandler.Create)))
	mux.Handle("DELETE /videos/{id}/comments/{commentID}", requireAuth(http.HandlerFunc(commentHandler.Delete)))
	mux.Handle("GET /history", requireAuth(http.HandlerFunc(historyHandler.List)))
	mux.Handle("GET /analytics/videos", requireAuth(http.HandlerFunc(analyticsHandler.Summary)))
	mux.Handle("DELETE /history/{id}", requireAuth(http.HandlerFunc(historyHandler.Clear)))
	mux.Handle("GET /playlists", requireAuth(http.HandlerFunc(playlistHandler.List)))
	mux.Handle("POST /playlists", requireAuth(http.HandlerFunc(playlistHandler.Create)))
	mux.Handle("GET /playlists/{id}", requireAuth(http.HandlerFunc(playlistHandler.Get)))
	mux.Handle("POST /playlists/{id}/videos", requireAuth(http.HandlerFunc(playlistHandler.Add)))
	mux.Handle("DELETE /playlists/{id}/videos", requireAuth(http.HandlerFunc(playlistHandler.Remove)))
	mux.Handle("GET /subscriptions", requireAuth(http.HandlerFunc(subscriptionHandler.List)))
	mux.Handle("POST /subscriptions/{creatorID}", requireAuth(http.HandlerFunc(subscriptionHandler.Subscribe)))
	mux.Handle("DELETE /subscriptions/{creatorID}", requireAuth(http.HandlerFunc(subscriptionHandler.Unsubscribe)))
	mux.Handle("GET /videos/{id}", requireAuth(http.HandlerFunc(videoHandler.Get)))
	mux.Handle("PATCH /videos/{id}", requireAuth(http.HandlerFunc(videoHandler.Update)))
	mux.Handle("DELETE /videos/{id}", requireAuth(http.HandlerFunc(videoHandler.Delete)))
	mux.Handle("GET /videos/{id}/stream", requireAuth(http.HandlerFunc(streamHandler.Stream)))
	mux.Handle("GET /videos/{id}/processing", requireAuth(http.HandlerFunc(processingHandler.Status)))
	mux.HandleFunc("GET /public/videos/{id}/stream", streamHandler.PublicStream)
	mux.Handle("POST /videos/{id}/upload/initiate", requireAuth(http.HandlerFunc(uploadHandler.Initiate)))
	mux.Handle("POST /videos/{id}/upload/complete", requireAuth(http.HandlerFunc(uploadHandler.Complete)))
	mux.Handle("POST /videos/{id}/upload/abort", requireAuth(http.HandlerFunc(uploadHandler.Abort)))
	mux.Handle("POST /videos/{id}/upload/multipart/initiate", requireAuth(http.HandlerFunc(uploadHandler.InitiateMultipart)))
	mux.Handle("PUT /videos/{id}/upload/multipart/{uploadID}/parts/{part}", requireAuth(http.HandlerFunc(uploadHandler.UploadPart)))
	mux.Handle("POST /videos/{id}/upload/multipart/{uploadID}/complete", requireAuth(http.HandlerFunc(uploadHandler.CompleteMultipart)))
	mux.Handle("DELETE /videos/{id}/upload/multipart/{uploadID}", requireAuth(http.HandlerFunc(uploadHandler.AbortMultipart)))

	if cleanup == nil {
		cleanup = func() {}
	}
	rateLimiter := middleware.NewRateLimiter(120, time.Minute)
	var rateLimited http.Handler = rateLimiter.MiddlewareExceptPaths(mux, "/auth/login")
	if cfg.QueueMode == "redis" {
		if redisLimiter, limiterErr := middleware.NewRedisRateLimiterWithScope(cfg.RedisURL, "global", 120, time.Minute); limiterErr == nil {
			rateLimited = redisLimiter.MiddlewareExceptPaths(mux, "/auth/login")
		} else {
			logger.Warn("redis rate limiter unavailable; using in-memory limiter", "error", limiterErr)
		}
	}
	protected := apiMetrics.Middleware(rateLimited)
	protected = middleware.SecurityHeaders(protected)
	traced := middleware.RequestLogger(logger, middleware.LocalCORS(protected))
	return otelhttp.NewHandler(traced, "http.server"), cleanup
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("writing json response failed", "error", err)
	}
}
