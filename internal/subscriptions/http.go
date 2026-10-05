package subscriptions

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yourusername/streamforge/internal/auth"
)

type HTTPHandler struct{ repository Repository }

func NewHTTPHandler(repository Repository) *HTTPHandler {
	return &HTTPHandler{repository: repository}
}

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	result, err := h.repository.List(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "subscriptions lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.repository.Subscribe(user.ID, r.PathValue("creatorID")); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusBadRequest, "cannot subscribe to this creator")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "subscription failed")
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *HTTPHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := h.repository.Unsubscribe(user.ID, r.PathValue("creatorID"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "subscription not found")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "unsubscription failed")
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
