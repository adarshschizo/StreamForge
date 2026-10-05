package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	APIAddr          string
	Environment      string
	Version          string
	DatabaseURL      string
	DatabaseMode     string
	RedisURL         string
	StorageURL       string
	JWTSecret        string
	CookieSecure     bool
	StorageMode      string
	QueueMode        string
	StorageAccessKey string
	StorageSecretKey string
	StorageUseSSL    bool
	FFprobePath      string
	FFmpegPath       string
	ProcessingOutput string
}

func Load() Config {
	_ = godotenv.Load()
	return Config{
		APIAddr:          get("STREAMFORGE_API_ADDR", ":8080"),
		Environment:      get("STREAMFORGE_ENV", "development"),
		Version:          get("STREAMFORGE_VERSION", "0.1.0"),
		DatabaseURL:      get("STREAMFORGE_DATABASE_URL", "postgres"+"://streamforge:streamforge@127.0.0.1:5432/streamforge?sslmode=disable"),
		DatabaseMode:     get("STREAMFORGE_DATABASE_MODE", "memory"),
		RedisURL:         get("STREAMFORGE_REDIS_URL", "redis://localhost:6379/0"),
		StorageURL:       get("STREAMFORGE_STORAGE_ENDPOINT", "http://127.0.0.1:9000"),
		JWTSecret:        get("STREAMFORGE_JWT_SECRET", "development-only-secret-change-me-32chars"),
		CookieSecure:     get("STREAMFORGE_COOKIE_SECURE", "false") == "true",
		StorageMode:      get("STREAMFORGE_STORAGE_MODE", "memory"),
		QueueMode:        get("STREAMFORGE_QUEUE_MODE", "memory"),
		StorageAccessKey: get("STREAMFORGE_STORAGE_ACCESS_KEY", "minioadmin"),
		StorageSecretKey: get("STREAMFORGE_STORAGE_SECRET_KEY", "minioadmin"),
		StorageUseSSL:    get("STREAMFORGE_STORAGE_USE_SSL", "false") == "true",
		FFprobePath:      get("STREAMFORGE_FFPROBE_PATH", "ffprobe"),
		FFmpegPath:       get("STREAMFORGE_FFMPEG_PATH", "ffmpeg"),
		ProcessingOutput: get("STREAMFORGE_PROCESSING_OUTPUT", "./data/processed"),
	}
}

func get(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
