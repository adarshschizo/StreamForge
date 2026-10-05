package videos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yourusername/streamforge/internal/auth"
)

func TestCreateAndListVideos(t *testing.T) {
	user := auth.User{ID: "user-1", Email: "user@example.com", Username: "user"}
	handler := NewHTTPHandler(NewMemoryRepository())

	request := httptest.NewRequest(http.MethodPost, "/videos", strings.NewReader(`{"title":"Launch video","visibility":"PUBLIC"}`))
	request = request.WithContext(auth.WithUser(context.Background(), user))
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/videos", nil)
	listRequest = listRequest.WithContext(auth.WithUser(context.Background(), user))
	listResponse := httptest.NewRecorder()
	handler.List(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "Launch video") {
		t.Fatalf("unexpected list response: %d %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestCreateAcceptsHumanReadableVisibility(t *testing.T) {
	user := auth.User{ID: "user-1", Email: "user@example.com", Username: "user"}
	handler := NewHTTPHandler(NewMemoryRepository())

	request := httptest.NewRequest(http.MethodPost, "/videos", strings.NewReader(`{"title":"My first video","visibility":" Private "}`))
	request = request.WithContext(auth.WithUser(context.Background(), user))
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"visibility":"PRIVATE"`) {
		t.Fatalf("expected normalized private visibility, got %s", response.Body.String())
	}
}

func TestUpdateVideoRejectsInvalidVisibility(t *testing.T) {
	repository := NewMemoryRepository()
	video, err := repository.Create("user-1", CreateInput{Title: "Demo", Visibility: VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(repository)
	request := httptest.NewRequest(http.MethodPatch, "/videos/"+video.ID, strings.NewReader(`{"visibility":"INVALID"}`))
	request.SetPathValue("id", video.ID)
	request = request.WithContext(auth.WithUser(context.Background(), auth.User{ID: "user-1"}))
	response := httptest.NewRecorder()
	handler.Update(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}
