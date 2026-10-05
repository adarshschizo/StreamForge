package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newIntegrationRedisQueue(t *testing.T) *RedisQueue {
	t.Helper()
	redisURL := os.Getenv("STREAMFORGE_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set STREAMFORGE_TEST_REDIS_URL to run Redis integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse STREAMFORGE_TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Fatalf("connect to integration Redis: %v", err)
	}

	suffix := uuid.NewString()
	q := &RedisQueue{
		client:           client,
		stream:           "streamforge:test:processing:" + suffix,
		deadLetterStream: "streamforge:test:dead:" + suffix,
		consumerGroup:    "streamforge-test-" + suffix,
		pendingMinIdle:   PendingJobMinIdle,
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := client.Del(cleanupCtx, q.stream, q.deadLetterStream).Err(); err != nil {
			t.Errorf("delete integration test streams: %v", err)
		}
		if err := client.Close(); err != nil {
			t.Errorf("close integration Redis client: %v", err)
		}
	})

	if err := q.EnsureConsumerGroup(ctx); err != nil {
		t.Fatalf("create integration consumer group: %v", err)
	}
	return q
}

func TestRedisQueueRetryAndDeadLetterLifecycle(t *testing.T) {
	q := newIntegrationRedisQueue(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	job := Job{
		ID: "job-" + uuid.NewString(), VideoID: "video-" + uuid.NewString(),
		InputKey: "originals/source.mp4", TraceParent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
	}
	if _, err := q.Publish(ctx, job); err != nil {
		t.Fatalf("publish initial job: %v", err)
	}

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		messageID, received, err := q.Read(ctx, fmt.Sprintf("consumer-%d", attempt), time.Second)
		if err != nil {
			t.Fatalf("read attempt %d: %v", attempt, err)
		}
		if received.ID != job.ID || received.TraceParent != job.TraceParent {
			t.Fatalf("read job = %+v, want original job ID and trace parent", received)
		}
		if err := q.RetryOrDeadLetter(ctx, messageID, received, errors.New("transient processing error")); err != nil {
			t.Fatalf("record failure on attempt %d: %v", attempt, err)
		}
	}

	entries, err := q.client.XRange(ctx, q.deadLetterStream, "-", "+").Result()
	if err != nil {
		t.Fatalf("read dead-letter stream: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dead-letter entries = %d, want 1", len(entries))
	}
	raw, ok := entries[0].Values["job"].(string)
	if !ok {
		t.Fatalf("dead-letter payload has type %T, want string", entries[0].Values["job"])
	}
	var deadLetter struct {
		Job   Job    `json:"job"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &deadLetter); err != nil {
		t.Fatalf("decode dead-letter payload: %v", err)
	}
	if deadLetter.Job.Attempts != MaxAttempts || deadLetter.Job.ID != job.ID {
		t.Fatalf("dead-letter job = %+v, want ID %q after %d attempts", deadLetter.Job, job.ID, MaxAttempts)
	}
	if deadLetter.Error != "transient processing error" {
		t.Fatalf("dead-letter error = %q, want original failure", deadLetter.Error)
	}
}

func TestRedisQueueReclaimsPendingJobAfterWorkerFailure(t *testing.T) {
	q := newIntegrationRedisQueue(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	job := Job{ID: "job-" + uuid.NewString(), VideoID: "video-" + uuid.NewString(), InputKey: "originals/source.mp4"}
	if _, err := q.Publish(ctx, job); err != nil {
		t.Fatalf("publish job: %v", err)
	}
	messageID, _, err := q.Read(ctx, "worker-that-crashed", time.Second)
	if err != nil {
		t.Fatalf("read job with crashed worker identity: %v", err)
	}

	q.pendingMinIdle = 0
	claimedID, claimedJob, err := q.Read(ctx, "recovery-worker", time.Second)
	if err != nil {
		t.Fatalf("claim pending job: %v", err)
	}
	if claimedID != messageID {
		t.Fatalf("reclaimed message ID = %q, want %q", claimedID, messageID)
	}
	if claimedJob.ID != job.ID {
		t.Fatalf("reclaimed job ID = %q, want %q", claimedJob.ID, job.ID)
	}
}

func TestRedisQueueRefreshPreventsReclaimingActiveJob(t *testing.T) {
	q := newIntegrationRedisQueue(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q.pendingMinIdle = time.Hour
	job := Job{ID: "job-" + uuid.NewString(), VideoID: "video-" + uuid.NewString(), InputKey: "originals/source.mp4"}
	if _, err := q.Publish(ctx, job); err != nil {
		t.Fatalf("publish job: %v", err)
	}
	messageID, _, err := q.Read(ctx, "active-worker", time.Second)
	if err != nil {
		t.Fatalf("read job with active worker: %v", err)
	}
	if err := q.RefreshPending(ctx, "active-worker", messageID); err != nil {
		t.Fatalf("refresh active job claim: %v", err)
	}

	if _, _, err := q.Read(ctx, "other-worker", time.Second); !errors.Is(err, ErrNoJob) {
		t.Fatalf("other worker read error = %v, want no claimable job", err)
	}
}
