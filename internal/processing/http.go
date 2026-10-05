package processing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/videos"
)

type HTTPHandler struct {
	videos videos.Repository
	db     StatusReader
}

type StatusReader interface {
	GetStatus(context.Context, string) (Status, error)
}

type Status struct {
	VideoID  string `json:"video_id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Stage    string `json:"stage"`
	Error    string `json:"error,omitempty"`
}

func NewHTTPHandler(repository videos.Repository, reader StatusReader) *HTTPHandler {
	return &HTTPHandler{videos: repository, db: reader}
}

func (h *HTTPHandler) Status(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if _, err := h.videos.GetForUser(user.ID, r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	if h.db == nil {
		writeJSON(w, http.StatusOK, Status{VideoID: r.PathValue("id"), Status: videos.StatusUploading, Stage: "queued"})
		return
	}
	status, err := h.db.GetStatus(r.Context(), r.PathValue("id"))
	if errors.Is(err, videos.ErrNotFound) {
		status = Status{VideoID: r.PathValue("id"), Status: videos.StatusUploading, Stage: "queued"}
	} else if err != nil {
		writeError(w, http.StatusBadGateway, "processing status unavailable")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
