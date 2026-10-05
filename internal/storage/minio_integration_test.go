package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestS3ServiceMinIOIntegration(t *testing.T) {
	endpoint := os.Getenv("STREAMFORGE_TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set STREAMFORGE_TEST_MINIO_ENDPOINT to run the MinIO integration test")
	}
	accessKey := envOrDefault("STREAMFORGE_TEST_MINIO_ACCESS_KEY", "streamforge")
	secretKey := envOrDefault("STREAMFORGE_TEST_MINIO_SECRET_KEY", "streamforge-secret")
	useSSL := strings.HasPrefix(endpoint, "https://")
	bucket := fmt.Sprintf("sf-test-%d", time.Now().UnixNano())

	service, err := NewS3(endpoint, accessKey, secretKey, bucket, useSSL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close S3 service: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for object := range service.client.ListObjects(cleanupCtx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err != nil {
				t.Errorf("list test objects for cleanup: %v", object.Err)
				continue
			}
			if err := service.client.RemoveObject(cleanupCtx, bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
				t.Errorf("remove test object %q: %v", object.Key, err)
			}
		}
		if err := service.client.RemoveBucket(cleanupCtx, bucket); err != nil {
			t.Errorf("remove test bucket: %v", err)
		}
	})

	original := []byte("streamforge-minio-integration")
	upload, err := service.Initiate(ctx, "uploads/presigned.mp4")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.UploadURL, bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("upload using presigned URL: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("presigned upload returned %s", response.Status)
	}

	object, err := service.Complete(ctx, upload.Key)
	if err != nil {
		t.Fatal(err)
	}
	if object.Size != int64(len(original)) {
		t.Fatalf("completed object size = %d, want %d", object.Size, len(original))
	}
	if _, err := service.Complete(ctx, "uploads/missing.mp4"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing object error = %v, want ErrObjectNotFound", err)
	}
	downloaded := filepath.Join(t.TempDir(), "download.mp4")
	if err := service.Download(ctx, upload.Key, downloaded); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, downloaded, original)

	source := filepath.Join(t.TempDir(), "master.m3u8")
	playlist := []byte("#EXTM3U\n#EXT-X-ENDLIST\n")
	if err := os.WriteFile(source, playlist, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.UploadFile(ctx, "videos/video-1/hls/master.m3u8", source, "application/vnd.apple.mpegurl"); err != nil {
		t.Fatal(err)
	}
	playbackURL, expiresAt, err := service.PlaybackURL(ctx, "video-1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(playbackURL, "X-Amz-Signature=") || !expiresAt.After(time.Now()) {
		t.Fatalf("invalid playback URL or expiry: %q, %s", playbackURL, expiresAt)
	}

	multipart, err := service.InitiateMultipart(ctx, "uploads/multipart.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UploadPart(ctx, multipart.ID, multipart.Key, 1, strings.NewReader("first-")); err != nil {
		t.Fatal(err)
	}
	if err := service.UploadPart(ctx, multipart.ID, multipart.Key, 2, strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	assembled, err := service.CompleteMultipart(ctx, multipart.ID, multipart.Key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Size != int64(len("first-second")) {
		t.Fatalf("multipart object size = %d, want %d", assembled.Size, len("first-second"))
	}
	multipartDownload := filepath.Join(t.TempDir(), "multipart.mp4")
	if err := service.Download(ctx, assembled.Key, multipartDownload); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, multipartDownload, []byte("first-second"))
}

func assertFileContents(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
