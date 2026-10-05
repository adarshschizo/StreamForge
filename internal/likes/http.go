package likes

import (
	"encoding/json"
	"net/http"

	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/videos"
)

type HTTPHandler struct {
	repository Repository
	videos     videos.Repository
}

func NewHTTPHandler(repository Repository, videoRepository videos.Repository) *HTTPHandler {
	return &HTTPHandler{repository: repository, videos: videoRepository}
}

func (h *HTTPHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.canLike(user.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	summary, err := h.repository.Toggle(user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "like operation failed")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *HTTPHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.canLike(user.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	summary, err := h.repository.Get(user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "like lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *HTTPHandler) canLike(userID, videoID string) bool {
	video, err := h.videos.GetByID(videoID)
	if err != nil || video.Status != videos.StatusReady {
		return false
	}
	if video.Visibility == videos.VisibilityPrivate {
		_, err = h.videos.GetForUser(userID, videoID)
		return err == nil
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
