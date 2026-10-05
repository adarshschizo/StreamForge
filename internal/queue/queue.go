package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	ProcessingStream     = "video-processing"
	DeadLetterStream     = "video-processing-dlq"
	ConsumerGroup        = "streamforge-workers"
	MaxAttempts          = 3
	PendingJobMinIdle    = 30 * time.Second
	ClaimRefreshInterval = 10 * time.Second
)

var ErrInvalidJob = errors.New("invalid processing job")
var ErrNoJob = redis.Nil

type Job struct {
	ID          string `json:"job_id"`
	VideoID     string `json:"video_id"`
	InputKey    string `json:"input_key"`
	Attempts    int    `json:"attempts"`
	TraceParent string `json:"traceparent,omitempty"`
	TraceState  string `json:"tracestate,omitempty"`
	Reclaimed   bool   `json:"-"`
}

func (j Job) Validate() error {
	if j.ID == "" || j.VideoID == "" || j.InputKey == "" {
		return ErrInvalidJob
	}
	if j.Attempts < 0 {
		return ErrInvalidJob
	}
	return nil
}

type RedisQueue struct {
	client           *redis.Client
	stream           string
	deadLetterStream string
	consumerGroup    string
	pendingMinIdle   time.Duration
}

type MemoryQueue struct {
	mu   sync.Mutex
	Jobs []Job
}

func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{}
}

func (q *MemoryQueue) Publish(_ context.Context, job Job) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Jobs = append(q.Jobs, job)
	return fmt.Sprintf("memory-%d", len(q.Jobs)), nil
}

func NewRedis(redisURL string) (*RedisQueue, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	return &RedisQueue{
		client:           redis.NewClient(options),
		stream:           ProcessingStream,
		deadLetterStream: DeadLetterStream,
		consumerGroup:    ConsumerGroup,
		pendingMinIdle:   PendingJobMinIdle,
	}, nil
}

func (q *RedisQueue) Close() error {
	return q.client.Close()
}

func (q *RedisQueue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

func (q *RedisQueue) Publish(ctx context.Context, job Job) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("encode processing job: %w", err)
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{"job": payload, "published_at": time.Now().UTC().Format(time.RFC3339Nano)},
	}).Result()
}

func (q *RedisQueue) EnsureConsumerGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.consumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

func (q *RedisQueue) Read(ctx context.Context, consumer string, block time.Duration) (string, Job, error) {
	if messageID, job, err := q.claimPending(ctx, consumer); err == nil {
		return messageID, job, nil
	} else if !errors.Is(err, redis.Nil) {
		return "", Job{}, err
	}
	messages, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    q.consumerGroup,
		Consumer: consumer,
		Streams:  []string{q.stream, ">"},
		Count:    1,
		Block:    block,
		NoAck:    false,
	}).Result()
	if err != nil {
		return "", Job{}, err
	}
	if len(messages) == 0 || len(messages[0].Messages) == 0 {
		return "", Job{}, redis.Nil
	}
	message := messages[0].Messages[0]
	raw, ok := message.Values["job"].(string)
	if !ok {
		if bytes, okBytes := message.Values["job"].([]byte); okBytes {
			raw = string(bytes)
		} else {
			return message.ID, Job{}, ErrInvalidJob
		}
	}
	var job Job
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return message.ID, Job{}, fmt.Errorf("decode processing job: %w", err)
	}
	if err := job.Validate(); err != nil {
		return message.ID, Job{}, err
	}
	job.Reclaimed = true
	return message.ID, job, nil
}

func (q *RedisQueue) claimPending(ctx context.Context, consumer string) (string, Job, error) {
	messages, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.consumerGroup,
		Consumer: consumer,
		MinIdle:  q.pendingMinIdle,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if err != nil {
		return "", Job{}, err
	}
	if len(messages) == 0 {
		return "", Job{}, redis.Nil
	}
	message := messages[0]
	raw, ok := message.Values["job"].(string)
	if !ok {
		if bytes, okBytes := message.Values["job"].([]byte); okBytes {
			raw = string(bytes)
		} else {
			return message.ID, Job{}, ErrInvalidJob
		}
	}
	var job Job
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return message.ID, Job{}, fmt.Errorf("decode claimed processing job: %w", err)
	}
	if err := job.Validate(); err != nil {
		return message.ID, Job{}, err
	}
	return message.ID, job, nil
}

func (q *RedisQueue) Ack(ctx context.Context, messageID string) error {
	return q.client.XAck(ctx, q.stream, q.consumerGroup, messageID).Err()
}

func (q *RedisQueue) RefreshPending(ctx context.Context, consumer, messageID string) error {
	messages, err := q.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   q.stream,
		Group:    q.consumerGroup,
		Consumer: consumer,
		MinIdle:  0,
		Messages: []string{messageID},
	}).Result()
	if err != nil {
		return fmt.Errorf("refresh pending processing job claim: %w", err)
	}
	if len(messages) == 0 {
		return fmt.Errorf("pending processing job %q is no longer in the consumer group", messageID)
	}
	return nil
}

func (q *RedisQueue) RetryOrDeadLetter(ctx context.Context, messageID string, job Job, cause error) error {
	if cause == nil {
		return errors.New("retry processing job requires a failure cause")
	}
	job.Attempts++
	payload, err := json.Marshal(struct {
		Job   Job    `json:"job"`
		Error string `json:"error"`
	}{Job: job, Error: cause.Error()})
	if err != nil {
		return fmt.Errorf("encode failed processing job: %w", err)
	}
	if job.Attempts >= MaxAttempts {
		if err := q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.deadLetterStream, Values: map[string]any{"job": payload}}).Err(); err != nil {
			return fmt.Errorf("publish job to dead-letter stream: %w", err)
		}
	} else {
		if _, err := q.Publish(ctx, job); err != nil {
			return fmt.Errorf("publish processing job retry: %w", err)
		}
	}
	if err := q.Ack(ctx, messageID); err != nil {
		return fmt.Errorf("ack failed processing job: %w", err)
	}
	return nil
}
