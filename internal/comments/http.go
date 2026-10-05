package comments

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.readyVideo(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.repository.List(r.PathValue("id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "comments lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.readyVideo(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	var input struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	comment, err := h.repository.Create(user.ID, r.PathValue("id"), input.Body)
	if errors.Is(err, ErrInvalid) {
		writeError(w, http.StatusBadRequest, "comment must be between 1 and 2000 characters")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "comment creation failed")
		return
	}
	comment.Username = user.Username
	writeJSON(w, http.StatusCreated, comment)
}

func (h *HTTPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.repository.Delete(user.ID, r.PathValue("commentID")); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "comment not found")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "comment deletion failed")
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *HTTPHandler) readyVideo(id string) bool {
	video, err := h.videos.GetByID(id)
	return err == nil && video.Status == videos.StatusReady && video.Visibility != videos.VisibilityPrivate
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
