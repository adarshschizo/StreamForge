package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type HTTPHandler struct {
	service      *Service
	cookieSecure bool
}

type registerRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func NewHTTPHandler(service *Service, cookieSecure bool) *HTTPHandler {
	return &HTTPHandler{service: service, cookieSecure: cookieSecure}
}

func (h *HTTPHandler) Register(w http.ResponseWriter, r *http.Request) {
	var input registerRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	user, token, err := h.service.Register(input.Email, input.Username, input.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailAlreadyUsed), errors.Is(err, ErrUsernameAlreadyUsed):
			writeError(w, http.StatusConflict, "user already exists")
		case errors.Is(err, ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "email, username, or password is invalid")
		default:
			writeError(w, http.StatusInternalServerError, "registration failed")
		}
		return
	}
	setSessionCookie(w, token, h.cookieSecure)
	writeJSON(w, http.StatusCreated, user)
}

func (h *HTTPHandler) Login(w http.ResponseWriter, r *http.Request) {
	var input loginRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	user, token, err := h.service.Login(input.Login, input.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	setSessionCookie(w, token, h.cookieSecure)
	writeJSON(w, http.StatusOK, user)
}

func (h *HTTPHandler) Logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "streamforge_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, user)
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

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     "streamforge_session",
		Value:    token,
		Path:     "/",
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
