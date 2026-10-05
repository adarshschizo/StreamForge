package streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/videos"
)

type fakeURLProvider struct{}

func (fakeURLProvider) PlaybackURL(context.Context, string, time.Duration) (string, time.Time, error) {
	return "http://storage.test/master.m3u8", time.Now().UTC().Add(time.Hour), nil
}

type readyRepository struct {
	videos.Repository
	video videos.Video
}

func (r readyRepository) GetForUser(string, string) (videos.Video, error) {
	return r.video, nil
}

func (r readyRepository) GetByID(string) (videos.Video, error) {
	return r.video, nil
}

func TestStreamReturnsPlaylistURLForReadyVideo(t *testing.T) {
	handler := NewHTTPHandler(readyRepository{video: videos.Video{
		ID: "video-1", UserID: "user-1", Status: videos.StatusReady,
	}}, fakeURLProvider{})
	request := httptest.NewRequest(http.MethodGet, "/videos/video-1/stream", nil)
	request.SetPathValue("id", "video-1")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "user-1"}))
	response := httptest.NewRecorder()

	handler.Stream(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "master.m3u8") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestStreamRejectsVideoThatIsNotReady(t *testing.T) {
	handler := NewHTTPHandler(readyRepository{video: videos.Video{
		ID: "video-1", UserID: "user-1", Status: videos.StatusProcessing,
	}}, fakeURLProvider{})
	request := httptest.NewRequest(http.MethodGet, "/videos/video-1/stream", nil)
	request.SetPathValue("id", "video-1")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "user-1"}))
	response := httptest.NewRecorder()

	handler.Stream(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", response.Code)
	}
}

func TestPublicStreamAllowsUnlistedReadyVideo(t *testing.T) {
	handler := NewHTTPHandler(readyRepository{video: videos.Video{
		ID: "video-1", Status: videos.StatusReady, Visibility: videos.VisibilityUnlisted,
	}}, fakeURLProvider{})
	request := httptest.NewRequest(http.MethodGet, "/public/videos/video-1/stream", nil)
	request.SetPathValue("id", "video-1")
	response := httptest.NewRecorder()

	handler.PublicStream(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "master.m3u8") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestPublicStreamRejectsPrivateVideo(t *testing.T) {
	handler := NewHTTPHandler(readyRepository{video: videos.Video{
		ID: "video-1", Status: videos.StatusReady, Visibility: videos.VisibilityPrivate,
	}}, fakeURLProvider{})
	request := httptest.NewRequest(http.MethodGet, "/public/videos/video-1/stream", nil)
	request.SetPathValue("id", "video-1")
	response := httptest.NewRecorder()

	handler.PublicStream(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
}
