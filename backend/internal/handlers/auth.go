package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
)

type AuthHandler struct{}

var authLimiter = security.NewLimiter(5, time.Minute, 5, 15*time.Minute)

type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Username string `json:"username"`
}

type AnonymousAuthRequest struct {
	Username string `json:"username"`
}

type AuthResponse struct {
	User  models.User `json:"user"`
	Token string      `json:"token"`
}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{}
}

func (h *AuthHandler) SignUp(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := strings.TrimSpace(req.Password)
	username := normalizeUsername(req.Username)

	if email == "" || password == "" || username == "" {
		writeAuthError(w, http.StatusBadRequest, "Email, password, and username are required")
		return
	}

	response, err := buildAuthResponse(email, username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}

	writeAuthJSON(w, http.StatusCreated, response)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := strings.TrimSpace(req.Password)
	username := normalizeUsername(req.Username)

	if email == "" || password == "" {
		writeAuthError(w, http.StatusBadRequest, "Email and password are required")
		return
	}

	if username == "" {
		username = normalizeUsername(usernameFromEmail(email))
	}
	if username == "" {
		username = "Guest"
	}

	response, err := buildAuthResponse(email, username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}

	writeAuthJSON(w, http.StatusOK, response)
}

func (h *AuthHandler) Anonymous(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}

	var req AnonymousAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	username := normalizeUsername(req.Username)
	if username == "" {
		username = fmt.Sprintf("Guest_%06d", time.Now().UTC().UnixNano()%1000000)
	}

	response, err := buildAuthResponse("", username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}

	writeAuthJSON(w, http.StatusOK, response)
}

func buildAuthResponse(email, username string) (AuthResponse, error) {
	token, err := newToken()
	if err != nil {
		return AuthResponse{}, err
	}

	user := models.User{
		ID:        fmt.Sprintf("user_%d", time.Now().UnixNano()),
		Username:  username,
		Email:     email,
		CreatedAt: time.Now().UTC(),
	}

	return AuthResponse{User: user, Token: token}, nil
}

func writeAuthJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAuthError(w http.ResponseWriter, code int, message string) {
	writeAuthJSON(w, code, map[string]string{"error": message})
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func usernameFromEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "Guest"
	}
	return parts[0]
}
