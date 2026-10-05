package playlists

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yourusername/streamforge/internal/auth"
)

type HTTPHandler struct{ repository Repository }

func NewHTTPHandler(repository Repository) *HTTPHandler { return &HTTPHandler{repository: repository} }

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, 401, "unauthorized")
		return
	}
	result, err := h.repository.List(user.ID)
	if err != nil {
		writeError(w, 500, "playlist lookup failed")
		return
	}
	writeJSON(w, 200, result)
}

func (h *HTTPHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, 401, "unauthorized")
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		writeError(w, 400, "invalid JSON request")
		return
	}
	item, err := h.repository.Create(user.ID, input.Name)
	if errors.Is(err, ErrInvalid) {
		writeError(w, 400, "playlist name is invalid")
		return
	}
	if err != nil {
		writeError(w, 500, "playlist creation failed")
		return
	}
	writeJSON(w, 201, item)
}

func (h *HTTPHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, 401, "unauthorized")
		return
	}
	item, err := h.repository.Get(user.ID, r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, 404, "playlist not found")
		return
	}
	if err != nil {
		writeError(w, 500, "playlist lookup failed")
		return
	}
	writeJSON(w, 200, item)
}

func (h *HTTPHandler) Add(w http.ResponseWriter, r *http.Request)    { h.change(w, r, true) }
func (h *HTTPHandler) Remove(w http.ResponseWriter, r *http.Request) { h.change(w, r, false) }
func (h *HTTPHandler) change(w http.ResponseWriter, r *http.Request, add bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, 401, "unauthorized")
		return
	}
	var input struct {
		VideoID string `json:"video_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || input.VideoID == "" {
		writeError(w, 400, "video_id is required")
		return
	}
	var err error
	if add {
		err = h.repository.AddVideo(user.ID, r.PathValue("id"), input.VideoID)
	} else {
		err = h.repository.RemoveVideo(user.ID, r.PathValue("id"), input.VideoID)
	}
	if errors.Is(err, ErrNotFound) {
		writeError(w, 404, "playlist or video not found")
		return
	}
	if err != nil {
		writeError(w, 500, "playlist update failed")
		return
	}
	w.WriteHeader(204)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
