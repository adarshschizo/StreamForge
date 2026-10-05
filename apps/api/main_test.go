package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/yourusername/streamforge/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	handler := newRouter(config.Config{Version: "test", Environment: "test", JWTSecret: "a-test-secret-that-is-at-least-32-chars"}, slog.Default())
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if got := response.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected response: %s", got)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	handler := newRouter(config.Config{JWTSecret: "a-test-secret-that-is-at-least-32-chars"}, slog.Default())
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", response.Code)
	}
}

func TestRouterCleanupClosesS3Workspaces(t *testing.T) {
	cfg := config.Config{
		JWTSecret:        "storage-cleanup-test-secret-with-at-least-32-chars",
		StorageMode:      "s3",
		StorageURL:       "http://127.0.0.1:9000",
		StorageAccessKey: "streamforge",
		StorageSecretKey: "streamforge-secret",
	}
	handler, cleanup := newRouterWithCleanup(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if handler == nil {
		t.Fatal("router is nil")
	}
	cleanup()
}

func TestAuthenticatedVideoPlaylistLifecycle(t *testing.T) {
	cfg := config.Config{
		Environment:  "test",
		Version:      "test",
		JWTSecret:    "integration-test-secret-with-at-least-32-characters",
		DatabaseMode: "memory",
		StorageMode:  "memory",
		QueueMode:    "memory",
	}
	handler, cleanup := newRouterWithCleanup(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cleanup()

	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	register := doJSONRequest(t, client, http.MethodPost, server.URL+"/auth/register", map[string]string{
		"email":    "integration@example.com",
		"username": "integration",
		"password": "correct horse battery staple",
	})
	if register.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", register.StatusCode, http.StatusCreated)
	}
	register.Body.Close()

	me, err := client.Get(server.URL + "/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	if me.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /auth/me status = %d, want %d", me.StatusCode, http.StatusOK)
	}
	me.Body.Close()

	videoResponse := doJSONRequest(t, client, http.MethodPost, server.URL+"/videos", map[string]string{
		"title":      "Integration video",
		"visibility": "PRIVATE",
	})
	if videoResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create video status = %d, want %d", videoResponse.StatusCode, http.StatusCreated)
	}
	var video struct {
		ID string `json:"id"`
	}
	decodeResponse(t, videoResponse, &video)
	if video.ID == "" {
		t.Fatal("create video response has no ID")
	}

	playlistResponse := doJSONRequest(t, client, http.MethodPost, server.URL+"/playlists", map[string]string{
		"name": "Saved videos",
	})
	if playlistResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create playlist status = %d, want %d", playlistResponse.StatusCode, http.StatusCreated)
	}
	var playlist struct {
		ID string `json:"id"`
	}
	decodeResponse(t, playlistResponse, &playlist)
	if playlist.ID == "" {
		t.Fatal("create playlist response has no ID")
	}

	addVideo := doJSONRequest(t, client, http.MethodPost, server.URL+"/playlists/"+playlist.ID+"/videos", map[string]string{
		"video_id": video.ID,
	})
	if addVideo.StatusCode != http.StatusNoContent {
		t.Fatalf("add video to playlist status = %d, want %d", addVideo.StatusCode, http.StatusNoContent)
	}
	addVideo.Body.Close()

	getPlaylist, err := client.Get(server.URL + "/playlists/" + playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if getPlaylist.StatusCode != http.StatusOK {
		t.Fatalf("get playlist status = %d, want %d", getPlaylist.StatusCode, http.StatusOK)
	}
	var playlistDetails struct {
		Videos []struct {
			ID string `json:"id"`
		} `json:"videos"`
	}
	decodeResponse(t, getPlaylist, &playlistDetails)
	if len(playlistDetails.Videos) != 1 || playlistDetails.Videos[0].ID != video.ID {
		t.Fatalf("playlist videos = %+v, want only video %q", playlistDetails.Videos, video.ID)
	}

	logout, err := client.Post(server.URL+"/auth/logout", "", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logout.StatusCode, http.StatusNoContent)
	}
	logout.Body.Close()

	unauthenticated, err := client.Get(server.URL + "/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout /auth/me status = %d, want %d", unauthenticated.StatusCode, http.StatusUnauthorized)
	}
	unauthenticated.Body.Close()
}

func doJSONRequest(t *testing.T, client *http.Client, method, url string, payload any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}
