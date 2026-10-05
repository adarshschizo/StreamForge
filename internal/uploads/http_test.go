package uploads

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/storage"
	"github.com/yourusername/streamforge/internal/videos"
)

func TestUploadLifecycleEnqueuesProcessingJob(t *testing.T) {
	videoRepository := videos.NewMemoryRepository()
	video, err := videoRepository.Create("user-1", videos.CreateInput{Title: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	publisher := queue.NewMemoryQueue()
	handler := NewHTTPHandler(videoRepository, storage.NewMemoryService(), publisher)
	ctx := auth.WithUser(context.Background(), auth.User{ID: "user-1"})

	initiate := httptest.NewRequest(http.MethodPost, "/videos/"+video.ID+"/upload/initiate", nil)
	initiate.SetPathValue("id", video.ID)
	initiate = initiate.WithContext(ctx)
	initiateResponse := httptest.NewRecorder()
	handler.Initiate(initiateResponse, initiate)
	if initiateResponse.Code != http.StatusCreated {
		t.Fatalf("expected initiate 201, got %d: %s", initiateResponse.Code, initiateResponse.Body.String())
	}

	complete := httptest.NewRequest(http.MethodPost, "/videos/"+video.ID+"/upload/complete", strings.NewReader("{}"))
	complete.SetPathValue("id", video.ID)
	complete = complete.WithContext(ctx)
	completeResponse := httptest.NewRecorder()
	handler.Complete(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("expected complete 200, got %d: %s", completeResponse.Code, completeResponse.Body.String())
	}
	if len(publisher.Jobs) != 1 || publisher.Jobs[0].VideoID != video.ID {
		t.Fatalf("expected one processing job, got %+v", publisher.Jobs)
	}
}
