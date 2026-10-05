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
	ProcessingStream = "video-processing"
	DeadLetterStream = "video-processing-dlq"
	ConsumerGroup    = "streamforge-workers"
	MaxAttempts      = 3
)

var ErrInvalidJob = errors.New("invalid processing job")
var ErrNoJob = redis.Nil

type Job struct {
	ID       string `json:"job_id"`
	VideoID  string `json:"video_id"`
	InputKey string `json:"input_key"`
	Attempts int    `json:"attempts"`
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
	client *redis.Client
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
	return &RedisQueue{client: redis.NewClient(options)}, nil
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
		Stream: ProcessingStream,
		Values: map[string]any{"job": payload, "published_at": time.Now().UTC().Format(time.RFC3339Nano)},
	}).Result()
}

func (q *RedisQueue) EnsureConsumerGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, ProcessingStream, ConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

func (q *RedisQueue) Read(ctx context.Context, consumer string, block time.Duration) (string, Job, error) {
	messages, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    ConsumerGroup,
		Consumer: consumer,
		Streams:  []string{ProcessingStream, ">"},
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
	return message.ID, job, nil
}

func (q *RedisQueue) Ack(ctx context.Context, messageID string) error {
	return q.client.XAck(ctx, ProcessingStream, ConsumerGroup, messageID).Err()
}

func (q *RedisQueue) RetryOrDeadLetter(ctx context.Context, messageID string, job Job, cause error) error {
	if err := q.Ack(ctx, messageID); err != nil {
		return fmt.Errorf("ack failed processing job: %w", err)
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
		return q.client.XAdd(ctx, &redis.XAddArgs{Stream: DeadLetterStream, Values: map[string]any{"job": payload}}).Err()
	}
	_, err = q.Publish(ctx, job)
	return err
}
