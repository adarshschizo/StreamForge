package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	OriginalsBucket = "streamforge-originals"
	ProcessedBucket = "streamforge-processed"
)

var ErrObjectNotFound = errors.New("object not found")

type Upload struct {
	ID        string    `json:"upload_id"`
	Key       string    `json:"key"`
	UploadURL string    `json:"upload_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Object struct {
	Key  string
	Size int64
}

type Service interface {
	Initiate(context.Context, string) (Upload, error)
	Complete(context.Context, string) (Object, error)
	Abort(context.Context, string) error
}

type MediaStore interface {
	Download(context.Context, string, string) error
	UploadFile(context.Context, string, string, string) error
}

type PlaybackStore interface {
	PlaybackURL(context.Context, string, time.Duration) (string, time.Time, error)
}

type MultipartUpload struct {
	ID        string    `json:"upload_id"`
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expires_at"`
}

type MultipartService interface {
	InitiateMultipart(context.Context, string) (MultipartUpload, error)
	UploadPart(context.Context, string, string, int, io.Reader) error
	CompleteMultipart(context.Context, string, string, int) (Object, error)
	AbortMultipart(context.Context, string) error
}

type MemoryService struct {
	mu      sync.Mutex
	uploads map[string]Upload
	objects map[string]Object
	parts   map[string]map[int][]byte
}

func NewMemoryService() *MemoryService {
	return &MemoryService{uploads: make(map[string]Upload), objects: make(map[string]Object), parts: make(map[string]map[int][]byte)}
}

func (s *MemoryService) Initiate(_ context.Context, key string) (Upload, error) {
	if strings.TrimSpace(key) == "" {
		return Upload{}, errors.New("storage key is required")
	}
	upload := Upload{
		ID:        fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Key:       key,
		UploadURL: "/storage/memory/" + url.PathEscape(key),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	s.mu.Lock()
	s.uploads[key] = upload
	s.mu.Unlock()
	return upload, nil
}

func (s *MemoryService) Complete(_ context.Context, key string) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.uploads[key]; !ok {
		return Object{}, ErrObjectNotFound
	}
	object := Object{Key: key}
	s.objects[key] = object
	return object, nil
}

func (s *MemoryService) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.uploads, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryService) Download(_ context.Context, key, destination string) error {
	return ErrObjectNotFound
}

func (s *MemoryService) UploadFile(_ context.Context, _, _, _ string) error {
	return errors.New("memory storage does not support media files")
}

func (s *MemoryService) PlaybackURL(_ context.Context, _ string, _ time.Duration) (string, time.Time, error) {
	return "", time.Time{}, errors.New("memory storage does not support playback URLs")
}

func (s *MemoryService) InitiateMultipart(_ context.Context, key string) (MultipartUpload, error) {
	if strings.TrimSpace(key) == "" {
		return MultipartUpload{}, errors.New("storage key is required")
	}
	id := fmt.Sprintf("memory-multipart-%d", time.Now().UnixNano())
	upload := MultipartUpload{ID: id, Key: key, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	s.mu.Lock()
	s.parts[id] = make(map[int][]byte)
	s.uploads[id] = Upload{ID: id, Key: key, ExpiresAt: upload.ExpiresAt}
	s.mu.Unlock()
	return upload, nil
}

func (s *MemoryService) UploadPart(_ context.Context, uploadID, key string, partNumber int, reader io.Reader) error {
	if partNumber < 1 {
		return errors.New("part number must be positive")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read upload part: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parts, ok := s.parts[uploadID]
	if !ok {
		return ErrObjectNotFound
	}
	if upload, ok := s.uploads[uploadID]; !ok || upload.Key != key {
		return ErrObjectNotFound
	}
	parts[partNumber] = data
	return nil
}

func (s *MemoryService) CompleteMultipart(_ context.Context, uploadID, key string, totalParts int) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parts, ok := s.parts[uploadID]
	if !ok || totalParts < 1 {
		return Object{}, ErrObjectNotFound
	}
	if upload, ok := s.uploads[uploadID]; !ok || upload.Key != key {
		return Object{}, ErrObjectNotFound
	}
	var size int64
	for i := 1; i <= totalParts; i++ {
		data, exists := parts[i]
		if !exists {
			return Object{}, fmt.Errorf("missing upload part %d", i)
		}
		size += int64(len(data))
	}
	s.objects[key] = Object{Key: key, Size: size}
	delete(s.parts, uploadID)
	delete(s.uploads, uploadID)
	return s.objects[key], nil
}

func (s *MemoryService) AbortMultipart(_ context.Context, uploadID string) error {
	s.mu.Lock()
	delete(s.parts, uploadID)
	delete(s.uploads, uploadID)
	s.mu.Unlock()
	return nil
}

type S3Service struct {
	client        *minio.Client
	bucket        string
	multipartRoot string
}

func NewS3(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3Service, error) {
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create object storage client: %w", err)
	}
	root, err := os.MkdirTemp("", "streamforge-multipart-*")
	if err != nil {
		return nil, fmt.Errorf("create multipart workspace: %w", err)
	}
	return &S3Service{client: client, bucket: bucket, multipartRoot: root}, nil
}

func (s *S3Service) Initiate(ctx context.Context, key string) (Upload, error) {
	if err := s.ensureBucket(ctx); err != nil {
		return Upload{}, err
	}
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	presigned, err := s.client.PresignedPutObject(ctx, s.bucket, key, 15*time.Minute)
	if err != nil {
		return Upload{}, fmt.Errorf("create upload URL: %w", err)
	}
	return Upload{ID: key, Key: key, UploadURL: presigned.String(), ExpiresAt: expiresAt}, nil
}

func (s *S3Service) Complete(ctx context.Context, key string) (Object, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Object{}, ErrObjectNotFound
	}
	return Object{Key: key, Size: info.Size}, nil
}

func (s *S3Service) Abort(_ context.Context, _ string) error {
	return nil
}

func (s *S3Service) Download(ctx context.Context, key, destination string) error {
	if err := s.ensureBucket(ctx); err != nil {
		return err
	}
	if err := s.client.FGetObject(ctx, s.bucket, key, destination, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("download object: %w", err)
	}
	return nil
}

func (s *S3Service) UploadFile(ctx context.Context, key, source, contentType string) error {
	if err := s.ensureBucket(ctx); err != nil {
		return err
	}
	_, err := s.client.FPutObject(ctx, s.bucket, key, source, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	return nil
}

func (s *S3Service) PlaybackURL(ctx context.Context, videoID string, expiry time.Duration) (string, time.Time, error) {
	if expiry <= 0 {
		return "", time.Time{}, errors.New("playback URL expiry must be positive")
	}

	if err := s.ensureBucket(ctx); err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(expiry)
	objectKey := fmt.Sprintf("videos/%s/hls/master.m3u8", videoID)
	url, err := s.client.PresignedGetObject(ctx, s.bucket, objectKey, expiry, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create playback URL: %w", err)
	}
	return url.String(), expiresAt, nil
}

func (s *S3Service) InitiateMultipart(_ context.Context, key string) (MultipartUpload, error) {
	if strings.TrimSpace(key) == "" {
		return MultipartUpload{}, errors.New("storage key is required")
	}
	id := fmt.Sprintf("multipart-%d", time.Now().UnixNano())
	if err := os.Mkdir(filepath.Join(s.multipartRoot, id), 0o700); err != nil {
		return MultipartUpload{}, fmt.Errorf("create multipart session: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.multipartRoot, id, "key"), []byte(key), 0o600); err != nil {
		return MultipartUpload{}, fmt.Errorf("create multipart manifest: %w", err)
	}
	return MultipartUpload{ID: id, Key: key, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}, nil
}

func (s *S3Service) UploadPart(_ context.Context, uploadID, key string, partNumber int, reader io.Reader) error {
	if partNumber < 1 {
		return errors.New("part number must be positive")
	}
	session := filepath.Join(s.multipartRoot, uploadID)
	if _, err := os.Stat(session); err != nil {
		return ErrObjectNotFound
	}
	manifest, err := os.ReadFile(filepath.Join(session, "key"))
	if err != nil || string(manifest) != key {
		return ErrObjectNotFound
	}
	file, err := os.OpenFile(filepath.Join(session, fmt.Sprintf("%08d.part", partNumber)), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create upload part: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("write upload part: %w", err)
	}
	return nil
}

func (s *S3Service) CompleteMultipart(ctx context.Context, uploadID, key string, totalParts int) (Object, error) {
	if totalParts < 1 {
		return Object{}, errors.New("total parts must be positive")
	}
	session := filepath.Join(s.multipartRoot, uploadID)
	manifest, err := os.ReadFile(filepath.Join(session, "key"))
	if err != nil || string(manifest) != key {
		return Object{}, ErrObjectNotFound
	}
	assembled, err := os.CreateTemp("", "streamforge-upload-*")
	if err != nil {
		return Object{}, fmt.Errorf("create assembled upload: %w", err)
	}
	assembledPath := assembled.Name()
	defer os.Remove(assembledPath)
	var size int64
	for i := 1; i <= totalParts; i++ {
		part, err := os.Open(filepath.Join(session, fmt.Sprintf("%08d.part", i)))
		if err != nil {
			assembled.Close()
			return Object{}, fmt.Errorf("missing upload part %d", i)
		}
		written, copyErr := io.Copy(assembled, part)
		part.Close()
		if copyErr != nil {
			assembled.Close()
			return Object{}, fmt.Errorf("assemble upload part %d: %w", i, copyErr)
		}
		size += written
	}
	if err := assembled.Close(); err != nil {
		return Object{}, fmt.Errorf("flush assembled upload: %w", err)
	}
	if err := s.UploadFile(ctx, key, assembledPath, "video/mp4"); err != nil {
		return Object{}, err
	}
	_ = os.RemoveAll(session)
	return Object{Key: key, Size: size}, nil
}

func (s *S3Service) AbortMultipart(_ context.Context, uploadID string) error {
	return os.RemoveAll(filepath.Join(s.multipartRoot, uploadID))
}

func (s *S3Service) ensureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check storage bucket: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create storage bucket: %w", err)
		}
	}
	return nil
}
