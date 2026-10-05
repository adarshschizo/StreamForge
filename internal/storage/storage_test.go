package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryMultipartUploadCanResumeAndComplete(t *testing.T) {
	service := NewMemoryService()
	upload, err := service.InitiateMultipart(context.Background(), "videos/1/original")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UploadPart(context.Background(), upload.ID, upload.Key, 2, bytes.NewBufferString("world")); err != nil {
		t.Fatal(err)
	}
	if err := service.UploadPart(context.Background(), upload.ID, upload.Key, 1, bytes.NewBufferString("hello ")); err != nil {
		t.Fatal(err)
	}
	object, err := service.CompleteMultipart(context.Background(), upload.ID, upload.Key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if object.Size != int64(len("hello world")) {
		t.Fatalf("expected size %d, got %d", len("hello world"), object.Size)
	}
}

func TestMemoryMultipartUploadRejectsWrongKey(t *testing.T) {
	service := NewMemoryService()
	upload, err := service.InitiateMultipart(context.Background(), "videos/1/original")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UploadPart(context.Background(), upload.ID, "videos/2/original", 1, bytes.NewBufferString("data")); err != ErrObjectNotFound {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestS3CompleteReportsStorageOutage(t *testing.T) {
	service, err := NewS3("http://127.0.0.1:1", "test", "test-secret", "test-bucket", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close S3 service: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = service.Complete(ctx, "uploads/video.mp4")
	if err == nil {
		t.Fatal("expected storage outage to be reported")
	}
	if errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("storage outage was incorrectly reported as a missing object: %v", err)
	}
}
