package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourusername/streamforge/internal/config"
	"github.com/yourusername/streamforge/internal/processing"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/storage"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	workerID, _ := os.Hostname()
	logger.Info("worker starting", "environment", cfg.Environment, "worker_id", workerID)
	if cfg.QueueMode != "redis" {
		logger.Error("worker requires Redis queue mode", "queue_mode", cfg.QueueMode)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	redisQueue, err := queue.NewRedis(cfg.RedisURL)
	if err != nil {
		logger.Error("worker queue setup failed", "error", err)
		os.Exit(1)
	}
	defer redisQueue.Close()

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := redisQueue.Ping(connectCtx); err != nil {
		cancel()
		logger.Error("worker cannot connect to redis", "error", err)
		os.Exit(1)
	}
	if err := redisQueue.EnsureConsumerGroup(connectCtx); err != nil {
		cancel()
		logger.Error("worker cannot create consumer group", "error", err)
		os.Exit(1)
	}
	cancel()

	statusStore := processing.StatusStore(processing.NoopStatusStore{})
	var databasePool *pgxpool.Pool
	if cfg.DatabaseMode == "postgres" {
		databasePool, err = pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			logger.Error("worker database setup failed", "error", err)
			os.Exit(1)
		}
		defer databasePool.Close()
		pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
		err = databasePool.Ping(pingCtx)
		pingCancel()
		if err != nil {
			logger.Error("worker cannot connect to database", "error", err)
			os.Exit(1)
		}
		statusStore = processing.NewPostgresStatusStore(databasePool)
	}

	sourceStore, destinationStore := storage.MediaStore(nil), storage.MediaStore(nil)
	if cfg.StorageMode == "s3" {
		sourceStore, err = storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.OriginalsBucket, cfg.StorageUseSSL)
		if err != nil {
			logger.Error("source storage setup failed", "error", err)
			os.Exit(1)
		}
		destinationStore, err = storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.ProcessedBucket, cfg.StorageUseSSL)
		if err != nil {
			logger.Error("processed storage setup failed", "error", err)
			os.Exit(1)
		}
	}
	processor := processing.Processor(processing.NewFFmpegProcessor(cfg.FFprobePath, cfg.FFmpegPath, cfg.ProcessingOutput, logger))
	if sourceStore != nil && destinationStore != nil {
		processor = processing.StorageProcessor{
			Source: sourceStore, Destination: destinationStore, LocalRoot: cfg.ProcessingOutput,
			Processor: processing.NewFFmpegProcessor(cfg.FFprobePath, cfg.FFmpegPath, cfg.ProcessingOutput, logger),
			Logger:    logger,
		}
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logger.Info("worker heartbeat", "worker_id", workerID)
		default:
			messageID, job, err := redisQueue.Read(ctx, workerID, time.Second)
			if err == nil {
				if err = statusStore.Start(ctx, job, workerID); err != nil {
					logger.Error("video processing status start failed", "job_id", job.ID, "error", err)
					if retryErr := redisQueue.RetryOrDeadLetter(ctx, messageID, job, err); retryErr != nil {
						logger.Error("video status start retry failed", "message_id", messageID, "error", retryErr)
					}
				} else {
					if progressErr := statusStore.UpdateProgress(ctx, job, 10, "preparing"); progressErr != nil {
						logger.Error("video processing progress update failed", "job_id", job.ID, "error", progressErr)
					}
					var result processing.Result
					if resultProcessor, ok := processor.(processing.ResultProcessor); ok {
						result, err = resultProcessor.ProcessResult(ctx, job)
					} else {
						err = processor.Process(ctx, job)
					}
					if err != nil {
						logger.Error("video processing failed", "message_id", messageID, "job_id", job.ID, "error", err)
						if statusErr := statusStore.Fail(ctx, job, err); statusErr != nil {
							logger.Error("video processing status failure update failed", "job_id", job.ID, "error", statusErr)
						}
						if retryErr := redisQueue.RetryOrDeadLetter(ctx, messageID, job, err); retryErr != nil {
							logger.Error("video job retry failed", "message_id", messageID, "error", retryErr)
						}
					} else {
						if progressErr := statusStore.UpdateProgress(ctx, job, 90, "finalizing"); progressErr != nil {
							logger.Error("video processing progress update failed", "job_id", job.ID, "error", progressErr)
						}
						if statusErr := statusStore.Complete(ctx, job, result); statusErr != nil {
							logger.Error("video processing status completion update failed", "job_id", job.ID, "error", statusErr)
						}
						if err := redisQueue.Ack(ctx, messageID); err != nil {
							logger.Error("video job acknowledgement failed", "message_id", messageID, "error", err)
						}
					}
				}
			} else if err != queue.ErrInvalidJob && err != context.Canceled {
				if err != queue.ErrNoJob {
					logger.Error("worker read failed", "error", err)
				}
			}
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return
		}
	}
}
