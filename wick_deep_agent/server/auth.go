package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type contextKey int

const userCtxKey contextKey = 0

// AuthUser represents an authenticated user (from gateway /auth/me).
type AuthUser struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// userFromContext returns the AuthUser from the request context.
func userFromContext(ctx context.Context) *AuthUser {
	u, _ := ctx.Value(userCtxKey).(*AuthUser)
	return u
}

// ResolveUser returns the username from the request context.
// Falls back to "local" when no auth user is present.
func ResolveUser(r *http.Request) string {
	u := userFromContext(r.Context())
	if u != nil {
		return u.Username
	}
	return "local"
}

// authMiddleware validates Bearer tokens against the gateway /auth/me endpoint.
// If gatewayURL is empty, auth is disabled and all requests pass through with username="local".
func authMiddleware(gatewayURL string, next http.Handler) http.Handler {
	if gatewayURL == "" {
		// No gateway: inject "local" user and pass through
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := &AuthUser{Username: "local", Role: "admin"}
			ctx := context.WithValue(r.Context(), userCtxKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	client := &http.Client{Timeout: 10 * time.Second}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := authHeader[7:]

		// Proxy to gateway /auth/me
		req, err := http.NewRequestWithContext(r.Context(), "GET", gatewayURL+"/auth/me", nil)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to create auth request")
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("auth proxy error: %v", err)
			writeJSONError(w, http.StatusBadGateway, "auth gateway unreachable")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to read auth response")
			return
		}

		var user AuthUser
		if err := json.Unmarshal(body, &user); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to parse auth response")
			return
		}

		ctx := context.WithValue(r.Context(), userCtxKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
