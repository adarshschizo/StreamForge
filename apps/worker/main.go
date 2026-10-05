package main

import (
	"context"
	"errors"
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
	"github.com/yourusername/streamforge/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	shutdownTrace, err := telemetry.Init(logger, cfg.OTELServiceName+"-worker", cfg.OTELExporterEndpoint, cfg.OTELEnabled)
	if err != nil {
		logger.Error("telemetry init failed", "error", err)
		os.Exit(1)
	}
	defer shutdownTrace()

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
		sourceS3, storeErr := storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.OriginalsBucket, cfg.StorageUseSSL)
		err = storeErr
		if err != nil {
			logger.Error("source storage setup failed", "error", err)
			os.Exit(1)
		}
		sourceStore = sourceS3
		defer func() {
			if closeErr := sourceS3.Close(); closeErr != nil {
				logger.Error("source storage cleanup failed", "error", closeErr)
			}
		}()

		destinationS3, storeErr := storage.NewS3(cfg.StorageURL, cfg.StorageAccessKey, cfg.StorageSecretKey, storage.ProcessedBucket, cfg.StorageUseSSL)
		err = storeErr
		if err != nil {
			logger.Error("processed storage setup failed", "error", err)
			os.Exit(1)
		}
		destinationStore = destinationS3
		defer func() {
			if closeErr := destinationS3.Close(); closeErr != nil {
				logger.Error("processed storage cleanup failed", "error", closeErr)
			}
		}()
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
				carrier := propagation.MapCarrier{}
				carrier.Set("traceparent", job.TraceParent)
				carrier.Set("tracestate", job.TraceState)
				jobCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)
				jobCtx, span := telemetry.StartJobSpan(jobCtx, "process", job.ID, job.VideoID)
				stopHeartbeat := startClaimHeartbeat(jobCtx, redisQueue, workerID, messageID, logger)
				if err = statusStore.Start(jobCtx, job, workerID); err != nil {
					if errors.Is(err, processing.ErrDuplicateJob) {
						logger.Info("duplicate processing job acknowledged", "job_id", job.ID)
						if ackErr := redisQueue.Ack(jobCtx, messageID); ackErr != nil {
							logger.Error("duplicate video job acknowledgement failed", "message_id", messageID, "error", ackErr)
						}
					} else {
						span.RecordError(err)
						span.SetStatus(codes.Error, "failed to start processing status")
						logger.Error("video processing status start failed", "job_id", job.ID, "error", err)
						if retryErr := redisQueue.RetryOrDeadLetter(jobCtx, messageID, job, err); retryErr != nil {
							logger.Error("video status start retry failed", "message_id", messageID, "error", retryErr)
						}
					}
				} else {
					if progressErr := statusStore.UpdateProgress(jobCtx, job, 10, "preparing"); progressErr != nil {
						logger.Error("video processing progress update failed", "job_id", job.ID, "error", progressErr)
					}
					var result processing.Result
					if resultProcessor, ok := processor.(processing.ResultProcessor); ok {
						result, err = resultProcessor.ProcessResult(jobCtx, job)
					} else {
						err = processor.Process(jobCtx, job)
					}
					if err != nil {
						span.RecordError(err)
						span.SetStatus(codes.Error, "video processing failed")
						logger.Error("video processing failed", "message_id", messageID, "job_id", job.ID, "error", err)
						if statusErr := statusStore.Fail(jobCtx, job, err); statusErr != nil {
							logger.Error("video processing status failure update failed", "job_id", job.ID, "error", statusErr)
						}
						if retryErr := redisQueue.RetryOrDeadLetter(jobCtx, messageID, job, err); retryErr != nil {
							logger.Error("video job retry failed", "message_id", messageID, "error", retryErr)
						}
					} else {
						if progressErr := statusStore.UpdateProgress(jobCtx, job, 90, "finalizing"); progressErr != nil {
							logger.Error("video processing progress update failed", "job_id", job.ID, "error", progressErr)
						}
						if statusErr := statusStore.Complete(jobCtx, job, result); statusErr != nil {
							logger.Error("video processing status completion update failed", "job_id", job.ID, "error", statusErr)
						}
						if err := redisQueue.Ack(jobCtx, messageID); err != nil {
							logger.Error("video job acknowledgement failed", "message_id", messageID, "error", err)
						}
					}
				}
				stopHeartbeat()
				span.End()
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

func startClaimHeartbeat(ctx context.Context, redisQueue *queue.RedisQueue, workerID, messageID string, logger *slog.Logger) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(queue.ClaimRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				refreshCtx, refreshCancel := context.WithTimeout(heartbeatCtx, 5*time.Second)
				if err := redisQueue.RefreshPending(refreshCtx, workerID, messageID); err != nil && heartbeatCtx.Err() == nil {
					logger.Error("worker could not refresh processing job claim", "message_id", messageID, "error", err)
				}
				refreshCancel()
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
