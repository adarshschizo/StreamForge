package history

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/yourusername/streamforge/internal/auth"
)

type HTTPHandler struct{ repository Repository }

func NewHTTPHandler(repository Repository) *HTTPHandler { return &HTTPHandler{repository: repository} }

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.repository.List(user.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) Clear(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := h.repository.Clear(user.ID, r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "history entry not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history deletion failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
