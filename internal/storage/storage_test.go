package storage

import (
	"bytes"
	"context"
	"testing"
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
