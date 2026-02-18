package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type contextKey int

const userContextKey contextKey = 0

// User represents an authenticated user in the system.
type User struct {
	Username string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Enabled      bool   `json:"enabled"`
	Source       string `json:"source"` // "local" or "oidc"
}

// oidcDiscoveryDoc holds cached OIDC provider endpoints.
type oidcDiscoveryDoc struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
}

// AuthService manages authentication, JWT tokens, and RBAC.
type AuthService struct {
	cfg        AuthConfig
	configPath string // path to config.yaml for persistence
	roles      map[string]RoleConfig
	jwtSecret  []byte
	expiry     time.Duration

	mu    sync.RWMutex
	users map[string]*User

	oauthClients map[string]*OAuthClientConfig

	oidcMu        sync.Mutex
	oidcDiscovery *oidcDiscoveryDoc
	oidcStates    map[string]time.Time

	// OnChange is called after any mutation (role/user CRUD) that changes config.
	// Set by the server to broadcast SSE events.
	OnChange func()
}

// NewAuthService creates a new AuthService from config.
func NewAuthService(cfg AuthConfig, roles map[string]RoleConfig, bootstrapUsers []UserConfig, oauthClients []OAuthClientConfig, configPath string) (*AuthService, error) {
	expiry, err := time.ParseDuration(cfg.TokenExpiry)
	if err != nil {
		return nil, fmt.Errorf("invalid token_expiry: %w", err)
	}

	svc := &AuthService{
		cfg:          cfg,
		configPath:   configPath,
		roles:        roles,
		jwtSecret:    []byte(cfg.JWTSecret),
		expiry:       expiry,
		users:        make(map[string]*User),
		oauthClients: make(map[string]*OAuthClientConfig),
		oidcStates:   make(map[string]time.Time),
	}

	for _, u := range bootstrapUsers {
		svc.users[u.Username] = &User{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
			Role:         u.Role,
			Enabled:      true,
			Source:       "local",
		}
	}

	for i := range oauthClients {
		svc.oauthClients[oauthClients[i].ClientID] = &oauthClients[i]
	}

	return svc, nil
}

// notifyChange fires the OnChange callback if set.
// Must be called OUTSIDE the mutex lock.
func (s *AuthService) notifyChange() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// persistConfig writes the current in-memory state back to config.yaml.
// Must be called with s.mu held (at least RLock).
func (s *AuthService) persistConfig() error {
	if s.configPath == "" {
		return nil
	}

	// Build a Config struct from current in-memory state.
	rolesCopy := make(map[string]RoleConfig, len(s.roles))
	for name, rc := range s.roles {
		cp := make([]string, len(rc.Tools))
		copy(cp, rc.Tools)
		rolesCopy[name] = RoleConfig{Tools: cp}
	}

	usersCopy := make([]UserConfig, 0, len(s.users))
	for _, u := range s.users {
		if u.Source == "oidc" {
			continue // don't persist OIDC-created users
		}
		usersCopy = append(usersCopy, UserConfig{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
			Role:         u.Role,
		})
	}

	oauthCopy := make([]OAuthClientConfig, 0, len(s.oauthClients))
	for _, oc := range s.oauthClients {
		oauthCopy = append(oauthCopy, *oc)
	}

	// Read existing config to preserve fields we don't manage (listen, downstream, etc.)
	existing, err := LoadConfig(s.configPath)
	if err != nil {
		return fmt.Errorf("reading existing config for merge: %w", err)
	}

	existing.Roles = rolesCopy
	existing.Users = usersCopy
	existing.OAuthClients = oauthCopy

	data, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	if err := os.WriteFile(s.configPath, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// VerifyPassword checks a username/password combination and returns the user if valid.
func (s *AuthService) VerifyPassword(username, password string) (*User, error) {
	s.mu.RLock()
	user, ok := s.users[username]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	if !user.Enabled {
		return nil, fmt.Errorf("user is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid password")
	}
	return user, nil
}

// ValidateClientCredentials checks an OAuth client_id / client_secret pair
// and returns a synthetic User so the rest of the auth pipeline works uniformly.
func (s *AuthService) ValidateClientCredentials(clientID, clientSecret string) (*User, error) {
	oc, ok := s.oauthClients[clientID]
	if !ok {
		return nil, fmt.Errorf("unknown client_id")
	}
	if oc.ClientSecret != clientSecret {
		return nil, fmt.Errorf("invalid client_secret")
	}
	return &User{
		Username: "oauth:" + clientID,
		Role:     oc.Role,
		Enabled:  true,
		Source:   "oauth_client",
	}, nil
}

// ExpirySeconds returns the token expiry duration in whole seconds.
func (s *AuthService) ExpirySeconds() int {
	return int(s.expiry.Seconds())
}

// GenerateToken creates a signed JWT for the given user.
func (s *AuthService) GenerateToken(user *User) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  user.Username,
		"role": user.Role,
		"iat":  now.Unix(),
		"exp":  now.Add(s.expiry).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// ValidateToken parses and validates a JWT, returning the associated user.
func (s *AuthService) ValidateToken(tokenStr string) (*User, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	username, ok := claims["sub"].(string)
	if !ok || username == "" {
		return nil, fmt.Errorf("missing sub claim")
	}

	s.mu.RLock()
	user, exists := s.users[username]
	s.mu.RUnlock()

	if !exists {
		// Check if this is an OAuth client token (sub = "oauth:<client_id>")
		if strings.HasPrefix(username, "oauth:") {
			clientID := strings.TrimPrefix(username, "oauth:")
			if oc, ok := s.oauthClients[clientID]; ok {
				return &User{
					Username: username,
					Role:     oc.Role,
					Enabled:  true,
					Source:   "oauth_client",
				}, nil
			}
		}
		return nil, fmt.Errorf("user %q no longer exists", username)
	}
	if !user.Enabled {
		return nil, fmt.Errorf("user %q is disabled", username)
	}

	return user, nil
}

// getRoleTools returns a copy of the tool patterns for the given role, with proper locking.
func (s *AuthService) getRoleTools(roleName string) ([]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.roles[roleName]
	if !ok {
		return nil, false
	}
	cp := make([]string, len(role.Tools))
	copy(cp, role.Tools)
	return cp, true
}

// IsToolAllowed checks if a user's role permits access to the named tool.
func (s *AuthService) IsToolAllowed(user *User, toolName string) bool {
	if user == nil {
		return false
	}
	tools, ok := s.getRoleTools(user.Role)
	if !ok {
		return false
	}
	for _, pattern := range tools {
		if pattern == "*" {
			return true
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(toolName, prefix) {
				return true
			}
		} else if pattern == toolName {
			return true
		}
	}
	return false
}

// FilterTools returns only the tools the user is permitted to access.
func (s *AuthService) FilterTools(user *User, tools []Tool) []Tool {
	if user == nil {
		return nil
	}
	roleTools, ok := s.getRoleTools(user.Role)
	if !ok {
		return nil
	}
	// Fast path: if role has "*", return all tools.
	for _, p := range roleTools {
		if p == "*" {
			return tools
		}
	}
	allowed := make([]Tool, 0)
	for _, t := range tools {
		if s.IsToolAllowed(user, t.Name) {
			allowed = append(allowed, t)
		}
	}
	return allowed
}

// ListRoles returns a deep copy of all roles.
func (s *AuthService) ListRoles() map[string]RoleConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]RoleConfig, len(s.roles))
	for name, rc := range s.roles {
		cp := make([]string, len(rc.Tools))
		copy(cp, rc.Tools)
		out[name] = RoleConfig{Tools: cp}
	}
	return out
}

// CreateRole adds a new role. Returns an error if it already exists.
func (s *AuthService) CreateRole(name string, tools []string) error {
	s.mu.Lock()
	if _, exists := s.roles[name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("role %q already exists", name)
	}
	s.roles[name] = RoleConfig{Tools: tools}
	if err := s.persistConfig(); err != nil {
		log.Printf("WARNING: role %q created in memory but failed to persist: %v", name, err)
	}
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// UpdateRole replaces the tool list for an existing role. Returns an error if it doesn't exist.
func (s *AuthService) UpdateRole(name string, tools []string) error {
	s.mu.Lock()
	if _, exists := s.roles[name]; !exists {
		s.mu.Unlock()
		return fmt.Errorf("role %q does not exist", name)
	}
	s.roles[name] = RoleConfig{Tools: tools}
	if err := s.persistConfig(); err != nil {
		log.Printf("WARNING: role %q updated in memory but failed to persist: %v", name, err)
	}
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// DeleteRole removes a role. Returns an error if it doesn't exist or if any user references it.
func (s *AuthService) DeleteRole(name string) error {
	s.mu.Lock()
	if _, exists := s.roles[name]; !exists {
		s.mu.Unlock()
		return fmt.Errorf("role %q does not exist", name)
	}
	for _, u := range s.users {
		if u.Role == name {
			s.mu.Unlock()
			return fmt.Errorf("cannot delete role %q: user %q is assigned to it", name, u.Username)
		}
	}
	delete(s.roles, name)
	if err := s.persistConfig(); err != nil {
		log.Printf("WARNING: role %q deleted in memory but failed to persist: %v", name, err)
	}
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// ListUsers returns all users (for admin API).
func (s *AuthService) ListUsers() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	return users
}

// CreateUser adds a new local user.
func (s *AuthService) CreateUser(username, passwordHash, role string) (*User, error) {
	if _, ok := s.roles[role]; !ok {
		return nil, fmt.Errorf("role %q does not exist", role)
	}

	s.mu.Lock()

	if _, exists := s.users[username]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("user %q already exists", username)
	}

	user := &User{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Enabled:      true,
		Source:       "local",
	}
	s.users[username] = user
	if err := s.persistConfig(); err != nil {
		log.Printf("WARNING: user %q created in memory but failed to persist: %v", username, err)
	}
	s.mu.Unlock()
	s.notifyChange()
	return user, nil
}

// DeleteUser removes a user by username.
func (s *AuthService) DeleteUser(username string) error {
	s.mu.Lock()

	if _, exists := s.users[username]; !exists {
		s.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	delete(s.users, username)
	if err := s.persistConfig(); err != nil {
		log.Printf("WARNING: user %q deleted in memory but failed to persist: %v", username, err)
	}
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// --- OIDC ---

// discoverOIDC fetches the OpenID Connect discovery document from the provider.
func (s *AuthService) discoverOIDC() (*oidcDiscoveryDoc, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()

	if s.oidcDiscovery != nil {
		return s.oidcDiscovery, nil
	}

	if s.cfg.OIDC == nil || s.cfg.OIDC.ProviderURL == "" {
		return nil, fmt.Errorf("OIDC is not configured")
	}

	url := strings.TrimRight(s.cfg.OIDC.ProviderURL, "/") + "/.well-known/openid-configuration"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading OIDC discovery response: %w", err)
	}

	var doc oidcDiscoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parsing OIDC discovery document: %w", err)
	}

	s.oidcDiscovery = &doc
	return &doc, nil
}

// OIDCAuthURL builds the authorization URL for the OIDC provider.
func (s *AuthService) OIDCAuthURL(state string) (string, error) {
	doc, err := s.discoverOIDC()
	if err != nil {
		return "", err
	}

	scopes := strings.Join(s.cfg.OIDC.Scopes, " ")
	url := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		doc.AuthorizationEndpoint,
		s.cfg.OIDC.ClientID,
		s.cfg.OIDC.RedirectURL,
		scopes,
		state,
	)
	return url, nil
}

// OIDCExchangeCode exchanges an authorization code for tokens and extracts the email.
func (s *AuthService) OIDCExchangeCode(code string) (string, error) {
	doc, err := s.discoverOIDC()
	if err != nil {
		return "", err
	}

	data := fmt.Sprintf("grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s&client_secret=%s",
		code,
		s.cfg.OIDC.RedirectURL,
		s.cfg.OIDC.ClientID,
		s.cfg.OIDC.ClientSecret,
	)

	resp, err := http.Post(doc.TokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("OIDC token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading OIDC token response: %w", err)
	}

	var tokenResp struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parsing OIDC token response: %w", err)
	}

	if tokenResp.IDToken == "" {
		return "", fmt.Errorf("no id_token in OIDC response")
	}

	// Parse the id_token to extract email (we skip signature verification
	// since we just received it directly from the provider over HTTPS).
	parts := strings.Split(tokenResp.IDToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid id_token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding id_token payload: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parsing id_token claims: %w", err)
	}

	email := claims.Email
	if email == "" {
		email = claims.Sub
	}
	if email == "" {
		return "", fmt.Errorf("no email or sub in id_token")
	}

	return email, nil
}

// FindOrCreateOIDCUser looks up or creates a user from an OIDC login.
func (s *AuthService) FindOrCreateOIDCUser(email string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user, ok := s.users[email]; ok {
		return user
	}

	defaultRole := "viewer"
	if s.cfg.OIDC != nil && s.cfg.OIDC.DefaultRole != "" {
		defaultRole = s.cfg.OIDC.DefaultRole
	}

	user := &User{
		Username: email,
		Role:     defaultRole,
		Enabled:  true,
		Source:   "oidc",
	}
	s.users[email] = user
	return user
}

// GenerateOIDCState creates a CSRF state token with a 5-minute TTL.
func (s *AuthService) GenerateOIDCState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.URLEncoding.EncodeToString(b)

	s.oidcMu.Lock()
	s.oidcStates[state] = time.Now().Add(5 * time.Minute)
	s.oidcMu.Unlock()

	return state, nil
}

// ValidateOIDCState checks and consumes a CSRF state token.
func (s *AuthService) ValidateOIDCState(state string) bool {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()

	expiry, ok := s.oidcStates[state]
	if !ok {
		return false
	}
	delete(s.oidcStates, state)
	return time.Now().Before(expiry)
}

// --- Context helpers ---

// userToContext stores a User in the request context.
func userToContext(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// userFromContext retrieves the authenticated User from the request, or nil.
func userFromContext(r *http.Request) *User {
	user, _ := r.Context().Value(userContextKey).(*User)
	return user
}
