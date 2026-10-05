package videos

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/yourusername/streamforge/internal/auth"
)

type HTTPHandler struct {
	repository Repository
}

func NewHTTPHandler(repository Repository) *HTTPHandler {
	return &HTTPHandler{repository: repository}
}

func (h *HTTPHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input CreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	video, err := h.repository.Create(user.ID, input)
	if err != nil {
		if errors.Is(err, ErrInvalidVideo) {
			writeError(w, http.StatusBadRequest, "title and visibility are invalid")
			return
		}
		slog.Error("creating video failed", "error", err)
		writeError(w, http.StatusInternalServerError, "video could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, video)
}

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, h.repository.ListByUser(user.ID))
}

func (h *HTTPHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	writeJSON(w, http.StatusOK, h.repository.Search(query, limit))
}

func (h *HTTPHandler) Get(w http.ResponseWriter, r *http.Request) {
	h.withVideo(w, r, func(video Video) {
		writeJSON(w, http.StatusOK, video)
	})
}

func (h *HTTPHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input UpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	video, err := h.repository.UpdateForUser(user.ID, r.PathValue("id"), input)
	if err != nil {
		h.writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, video)
}

func (h *HTTPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.repository.DeleteForUser(user.ID, r.PathValue("id")); err != nil {
		h.writeRepositoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) withVideo(w http.ResponseWriter, r *http.Request, callback func(Video)) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.repository.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		h.writeRepositoryError(w, err)
		return
	}
	callback(video)
}

func (h *HTTPHandler) writeRepositoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotOwner):
		writeError(w, http.StatusNotFound, "video not found")
	case errors.Is(err, ErrInvalidVideo):
		writeError(w, http.StatusBadRequest, "video fields are invalid")
	default:
		writeError(w, http.StatusInternalServerError, "video operation failed")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
