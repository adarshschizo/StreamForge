package analytics

import (
	"encoding/json"
	"net/http"

	"github.com/yourusername/streamforge/internal/auth"
)

type HTTPHandler struct{ repository Repository }

func NewHTTPHandler(repository Repository) *HTTPHandler { return &HTTPHandler{repository: repository} }

func (h *HTTPHandler) Summary(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	result, err := h.repository.OwnerSummary(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "analytics lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
