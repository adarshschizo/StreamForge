package queue

import (
	"context"
	"strings"
	"testing"
)

func TestJobValidation(t *testing.T) {
	if err := (Job{ID: "job", VideoID: "video", InputKey: "originals/video.mp4"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Job{ID: "job"}).Validate(); err != ErrInvalidJob {
		t.Fatalf("expected invalid job, got %v", err)
	}
}

func TestRetryOrDeadLetterRequiresFailureCause(t *testing.T) {
	q := &RedisQueue{}
	err := q.RetryOrDeadLetter(context.Background(), "message", Job{}, nil)
	if err == nil {
		t.Fatal("RetryOrDeadLetter accepted a nil failure cause")
	}
	if !strings.Contains(err.Error(), "requires a failure cause") {
		t.Fatalf("expected a failure-cause validation error, got %v", err)
	}
}
