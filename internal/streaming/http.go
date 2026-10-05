package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/videos"
)

type URLProvider interface {
	PlaybackURL(context.Context, string, time.Duration) (string, time.Time, error)
}

type HTTPHandler struct {
	videos  videos.Repository
	storage URLProvider
}

func NewHTTPHandler(videoRepository videos.Repository, provider URLProvider) *HTTPHandler {
	return &HTTPHandler{videos: videoRepository, storage: provider}
}

func (h *HTTPHandler) Stream(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, videos.ErrNotFound) || errors.Is(err, videos.ErrNotOwner) {
			writeError(w, http.StatusNotFound, "video not found")
			return
		}
		writeError(w, http.StatusBadRequest, "video lookup failed")
		return
	}
	h.writePlaylist(w, r, video)
}

func (h *HTTPHandler) PublicStream(w http.ResponseWriter, r *http.Request) {
	video, err := h.videos.GetByID(r.PathValue("id"))
	if err != nil || video.Visibility == videos.VisibilityPrivate {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	h.writePlaylist(w, r, video)
}

func (h *HTTPHandler) writePlaylist(w http.ResponseWriter, r *http.Request, video videos.Video) {
	if video.Status != videos.StatusReady {
		writeError(w, http.StatusConflict, "video is not ready for streaming")
		return
	}
	if h.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming storage is not configured")
		return
	}
	url, expiresAt, err := h.storage.PlaybackURL(r.Context(), video.ID, time.Hour)
	if err != nil {
		writeError(w, http.StatusBadGateway, "streaming storage unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"video_id": video.ID, "playlist_url": url, "expires_at": expiresAt})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
