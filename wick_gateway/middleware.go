package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// AuthMiddleware extracts and validates JWT tokens for protected routes.
// If authSvc is nil (auth disabled), all requests pass through unchanged.
func AuthMiddleware(authSvc *AuthService, resourceURL string, next http.Handler) http.Handler {
	if authSvc == nil {
		return next
	}

	wwwAuth := `Bearer resource_metadata="` + resourceURL + `/.well-known/oauth-protected-resource"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public routes — no JWT required.
		if isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}

		tokenStr := extractBearerToken(r)
		if tokenStr == "" {
			w.Header().Set("WWW-Authenticate", wwwAuth)
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		user, err := authSvc.ValidateToken(tokenStr)
		if err != nil {
			w.Header().Set("WWW-Authenticate", wwwAuth)
			writeJSONError(w, http.StatusUnauthorized, "invalid token: "+err.Error())
			return
		}

		r = r.WithContext(userToContext(r.Context(), user))
		next.ServeHTTP(w, r)
	})
}

// isPublicRoute returns true for routes that don't require authentication.
func isPublicRoute(r *http.Request) bool {
	path := r.URL.Path

	switch {
	case path == "/" && r.Method == http.MethodGet:
		return true
	case path == "/auth/login" && r.Method == http.MethodPost:
		return true
	case path == "/auth/oidc/login" && r.Method == http.MethodGet:
		return true
	case path == "/auth/oidc/callback" && r.Method == http.MethodGet:
		return true
	case path == "/.well-known/oauth-protected-resource" && r.Method == http.MethodGet:
		return true
	case path == "/.well-known/oauth-authorization-server" && r.Method == http.MethodGet:
		return true
	case path == "/oauth/token" && r.Method == http.MethodPost:
		return true
	case path == "/authorize":
		return true
	case path == "/api/events" && r.Method == http.MethodGet:
		return true
	}
	return false
}

// extractBearerToken pulls the token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// requireAdmin checks if the current user has the admin role.
// Returns the user if admin, or nil after writing a 403 response.
func requireAdmin(w http.ResponseWriter, r *http.Request) *User {
	user := userFromContext(r)
	if user == nil || user.Role != "admin" {
		writeJSONError(w, http.StatusForbidden, "admin access required")
		return nil
	}
	return user
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
