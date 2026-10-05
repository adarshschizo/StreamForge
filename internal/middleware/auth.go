package middleware

import (
	"net/http"

	"github.com/yourusername/streamforge/internal/auth"
)

func RequireAuth(service *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := service.AuthenticateTokenFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
	})
}
