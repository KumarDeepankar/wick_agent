package wickserver

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

// ResolveRole returns the role from the request context.
// Falls back to "admin" when no auth user is present (local mode).
func ResolveRole(r *http.Request) string {
	u := userFromContext(r.Context())
	if u != nil {
		return u.Role
	}
	return "admin"
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

// authProxy returns a handler that reverse-proxies auth routes (/auth/login,
// /auth/me) to the gateway, so the UI can call them as same-origin requests.
func authProxy(gatewayURL string) http.Handler {
	client := &http.Client{Timeout: 10 * time.Second}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetURL := gatewayURL + r.URL.Path

		var bodyReader io.Reader
		if r.Body != nil {
			bodyReader = r.Body
			defer r.Body.Close()
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to create proxy request")
			return
		}

		// Copy relevant headers
		if ct := r.Header.Get("Content-Type"); ct != "" {
			proxyReq.Header.Set("Content-Type", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			proxyReq.Header.Set("Authorization", auth)
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			log.Printf("auth proxy error: %v", err)
			writeJSONError(w, http.StatusBadGateway, "auth gateway unreachable")
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to read gateway response")
			return
		}

		// Copy response content-type and status from gateway
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	})
}
