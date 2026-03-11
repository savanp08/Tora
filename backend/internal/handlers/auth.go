package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	jwtutil "github.com/savanp08/converse/internal/auth"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"

	"github.com/gocql/gocql"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	scylla *database.ScyllaStore
}

var authLimiter = security.NewLimiter(5, time.Minute, 5, 15*time.Minute)
var authForgotPasswordRequestLimiter = security.NewLimiter(5, time.Minute, 8, 20*time.Minute)
var authForgotPasswordVerifyLimiter = security.NewLimiter(8, time.Minute, 12, 20*time.Minute)

const (
	authCookieName                    = "tora_auth"
	maxAuthUsernameLength             = 32
	passwordResetOTPLength            = 6
	defaultPasswordResetOTPTTLMinutes = 10
	minPasswordResetOTPTTLMinutes     = 1
	maxPasswordResetOTPTTLMinutes     = 60
	defaultPasswordResetSMTPPort      = 587
	passwordResetSMTPSubject          = "Converse password reset code"
	passwordResetDebugOTPEnv          = "AUTH_PASSWORD_RESET_DEBUG_OTP"
	passwordResetOTPTTLMinutesEnv     = "AUTH_PASSWORD_RESET_OTP_TTL_MINUTES"
	passwordResetSMTPHostEnv          = "SMTP_HOST"
	passwordResetSMTPPortEnv          = "SMTP_PORT"
	passwordResetSMTPUsernameEnv      = "SMTP_USERNAME"
	passwordResetSMTPPasswordEnv      = "SMTP_PASSWORD"
	passwordResetSMTPFromAddressEnv   = "SMTP_FROM"
	passwordResetSMTPFromNameEnv      = "SMTP_FROM_NAME"
)

type RegisterRequest struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	FullName  string `json:"fullName"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AnonymousAuthRequest struct {
	Username string `json:"username"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ForgotPasswordVerifyRequest struct {
	Email       string `json:"email"`
	OTP         string `json:"otp"`
	NewPassword string `json:"newPassword"`
}

type AuthResponse struct {
	User  models.User `json:"user"`
	Token string      `json:"token"`
}

type ForgotPasswordRequestResponse struct {
	Message   string `json:"message"`
	DebugOTP  string `json:"debugOtp,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

type ForgotPasswordVerifyResponse struct {
	Message         string `json:"message"`
	PasswordUpdated bool   `json:"passwordUpdated"`
}

type PasswordResetTokenRecord struct {
	Email     string
	OTPHash   string
	ExpiresAt time.Time
	CreatedAt time.Time
	Consumed  bool
}

type passwordResetSMTPSettings struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

func NewAuthHandler(scyllaStore *database.ScyllaStore) *AuthHandler {
	handler := &AuthHandler{scylla: scyllaStore}
	handler.ensureUserSchema()
	handler.ensurePasswordResetSchema()
	return handler
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeAuthError(w, http.StatusBadRequest, "Valid email is required")
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeAuthError(w, http.StatusBadRequest, "Password is required")
		return
	}
	username := normalizeAccountUsername(req.Username)
	if username == "" {
		writeAuthError(
			w,
			http.StatusBadRequest,
			"Username is required (letters, numbers, spaces, dashes, and underscores only)",
		)
		return
	}
	fullName := normalizeAuthDisplayName(req.FullName, req.Username, email)

	ctx := r.Context()
	_, exists, err := h.getUserByEmail(ctx, email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to validate account")
		return
	}
	if exists {
		writeAuthError(w, http.StatusConflict, "Email is already registered")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to secure password")
		return
	}

	userID, err := gocql.RandomUUID()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to create user id")
		return
	}
	now := time.Now().UTC()
	user := models.User{
		ID:           userID,
		Email:        email,
		PasswordHash: string(passwordHash),
		GoogleID:     "",
		Username:     username,
		FullName:     fullName,
		AvatarURL:    strings.TrimSpace(req.AvatarURL),
		CreatedAt:    now,
	}

	reserved, err := h.reserveUsername(ctx, username, user.ID, user.CreatedAt)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to reserve username")
		return
	}
	if !reserved {
		writeAuthError(w, http.StatusConflict, "Username is already taken")
		return
	}

	if err := h.insertUser(ctx, user); err != nil {
		_ = h.releaseUsername(ctx, username, user.ID)
		writeAuthError(w, http.StatusInternalServerError, "Failed to create account")
		return
	}

	token, err := jwtutil.GenerateToken(user.ID.String(), user.Email, user.Username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}
	setAuthCookie(w, r, token)
	writeAuthJSON(w, http.StatusCreated, AuthResponse{User: user, Token: token})
}

func (h *AuthHandler) SignUp(w http.ResponseWriter, r *http.Request) {
	h.Register(w, r)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || strings.TrimSpace(req.Password) == "" {
		writeAuthError(w, http.StatusBadRequest, "Email and password are required")
		return
	}

	user, exists, err := h.getUserByEmail(r.Context(), email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to load account")
		return
	}
	if !exists || strings.TrimSpace(user.PasswordHash) == "" {
		writeAuthError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeAuthError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	user, err = h.ensureUserHasUsername(r.Context(), user)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to finalize account")
		return
	}

	token, err := jwtutil.GenerateToken(user.ID.String(), user.Email, user.Username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}
	setAuthCookie(w, r, token)
	writeAuthJSON(w, http.StatusOK, AuthResponse{User: user, Token: token})
}

func (h *AuthHandler) ForgotPasswordRequest(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authForgotPasswordRequestLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Password reset rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	var req ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !isLikelyEmail(email) {
		writeAuthError(w, http.StatusBadRequest, "Valid email is required")
		return
	}

	response := ForgotPasswordRequestResponse{
		Message: "If an account exists for this email, an OTP has been sent.",
	}

	user, exists, err := h.getUserByEmail(r.Context(), email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to process password reset request")
		return
	}
	if !exists || strings.TrimSpace(user.Email) == "" {
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	otp, err := newNumericOTP(passwordResetOTPLength)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate OTP")
		return
	}
	otpHash, err := bcrypt.GenerateFromPassword([]byte(otp), bcrypt.DefaultCost)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to secure OTP")
		return
	}

	ttl := loadPasswordResetOTPTTL()
	expiresAt := time.Now().UTC().Add(ttl)
	if err := h.upsertPasswordResetToken(r.Context(), email, string(otpHash), expiresAt, clientIP, ttl); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to prepare OTP")
		return
	}

	if err := sendPasswordResetEmail(email, otp, expiresAt); err != nil {
		log.Printf("[auth] password-reset email delivery failed: %v", err)
	}

	if shouldExposePasswordResetDebugOTP() {
		response.DebugOTP = otp
		response.ExpiresAt = expiresAt.Unix()
	}
	writeAuthJSON(w, http.StatusOK, response)
}

func (h *AuthHandler) ForgotPasswordVerify(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authForgotPasswordVerifyLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Password reset verification rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	var req ForgotPasswordVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !isLikelyEmail(email) {
		writeAuthError(w, http.StatusBadRequest, "Valid email is required")
		return
	}
	otp := normalizeNumericOTP(req.OTP)
	if len(otp) != passwordResetOTPLength {
		writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("OTP must be %d digits", passwordResetOTPLength))
		return
	}

	tokenRecord, exists, err := h.getPasswordResetTokenByEmail(r.Context(), email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to verify OTP")
		return
	}
	if !exists || tokenRecord.Consumed || tokenRecord.ExpiresAt.Before(time.Now().UTC()) {
		_ = h.deletePasswordResetToken(r.Context(), email)
		writeAuthError(w, http.StatusUnauthorized, "Invalid or expired OTP")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(tokenRecord.OTPHash), []byte(otp)); err != nil {
		writeAuthError(w, http.StatusUnauthorized, "Invalid or expired OTP")
		return
	}

	newPassword := strings.TrimSpace(req.NewPassword)
	passwordUpdated := false
	if newPassword != "" {
		user, userExists, userErr := h.getUserByEmail(r.Context(), email)
		if userErr != nil {
			writeAuthError(w, http.StatusInternalServerError, "Failed to update password")
			return
		}
		if !userExists {
			_ = h.deletePasswordResetToken(r.Context(), email)
			writeAuthError(w, http.StatusUnauthorized, "Invalid or expired OTP")
			return
		}
		passwordHash, hashErr := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if hashErr != nil {
			writeAuthError(w, http.StatusInternalServerError, "Failed to secure password")
			return
		}
		if updateErr := h.updateUserPasswordHash(r.Context(), user.ID, string(passwordHash)); updateErr != nil {
			writeAuthError(w, http.StatusInternalServerError, "Failed to update password")
			return
		}
		passwordUpdated = true
	}

	if err := h.deletePasswordResetToken(r.Context(), email); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to finalize password reset")
		return
	}

	message := "OTP verified. Existing password is unchanged."
	if passwordUpdated {
		message = "Password reset successful. You can sign in with your new password."
	}
	writeAuthJSON(w, http.StatusOK, ForgotPasswordVerifyResponse{
		Message:         message,
		PasswordUpdated: passwordUpdated,
	})
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

	fullName := normalizeAuthDisplayName(req.Username, req.Username, "")
	userID, err := gocql.RandomUUID()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}
	user := models.User{
		ID:           userID,
		Email:        "",
		PasswordHash: "",
		GoogleID:     "",
		Username:     "",
		FullName:     fullName,
		AvatarURL:    "",
		CreatedAt:    time.Now().UTC(),
	}

	token, err := jwtutil.GenerateToken(user.ID.String(), "", "")
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}

	setAuthCookie(w, r, token)
	writeAuthJSON(w, http.StatusOK, AuthResponse{User: user, Token: token})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w, r)
	writeAuthJSON(w, http.StatusOK, map[string]string{"message": "Logged out"})
}

func (h *AuthHandler) ensureUserSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	usersTable := h.scylla.Table("users")
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id uuid PRIMARY KEY,
		email text,
		password_hash text,
		google_id text,
		username text,
		full_name text,
		avatar_url text,
		created_at timestamp
	)`, usersTable)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[auth] ensure users schema failed: %v", err)
		return
	}

	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD username text`, usersTable),
	}
	for _, query := range alterQueries {
		if err := h.scylla.Session.Query(query).Exec(); err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			log.Printf("[auth] ensure users alter failed: %v", err)
		}
	}

	indexQueries := []string{
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (email)`, usersTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (google_id)`, usersTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (username)`, usersTable),
	}
	for _, query := range indexQueries {
		if err := h.scylla.Session.Query(query).Exec(); err != nil {
			log.Printf("[auth] ensure users index failed: %v", err)
		}
	}

	usernamesTable := h.scylla.Table("users_by_username")
	usernamesQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		username text PRIMARY KEY,
		user_id uuid,
		created_at timestamp
	)`, usernamesTable)
	if err := h.scylla.Session.Query(usernamesQuery).Exec(); err != nil {
		log.Printf("[auth] ensure users_by_username schema failed: %v", err)
	}
}

func (h *AuthHandler) ensurePasswordResetSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	resetTable := h.scylla.Table("password_reset_tokens")
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		email text PRIMARY KEY,
		otp_hash text,
		expires_at timestamp,
		created_at timestamp,
		consumed_at timestamp,
		request_ip text
	)`, resetTable)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[auth] ensure password_reset_tokens schema failed: %v", err)
	}
}

func (h *AuthHandler) upsertPasswordResetToken(
	ctx context.Context,
	email string,
	otpHash string,
	expiresAt time.Time,
	requestIP string,
	ttl time.Duration,
) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return fmt.Errorf("email is required")
	}
	trimmedOTPHash := strings.TrimSpace(otpHash)
	if trimmedOTPHash == "" {
		return fmt.Errorf("otp hash is required")
	}

	resetTable := h.scylla.Table("password_reset_tokens")
	query := fmt.Sprintf(
		`INSERT INTO %s (email, otp_hash, expires_at, created_at, request_ip) VALUES (?, ?, ?, ?, ?) USING TTL ?`,
		resetTable,
	)
	now := time.Now().UTC()
	ttlSeconds := int(ttl.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = int((defaultPasswordResetOTPTTLMinutes * time.Minute).Seconds())
	}
	return h.scylla.Session.Query(
		query,
		normalizedEmail,
		trimmedOTPHash,
		expiresAt.UTC(),
		now,
		strings.TrimSpace(requestIP),
		ttlSeconds,
	).WithContext(ctx).Exec()
}

func (h *AuthHandler) getPasswordResetTokenByEmail(
	ctx context.Context,
	email string,
) (PasswordResetTokenRecord, bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return PasswordResetTokenRecord{}, false, fmt.Errorf("scylla session is not configured")
	}
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return PasswordResetTokenRecord{}, false, nil
	}

	resetTable := h.scylla.Table("password_reset_tokens")
	query := fmt.Sprintf(
		`SELECT email, otp_hash, expires_at, created_at, consumed_at FROM %s WHERE email = ? LIMIT 1`,
		resetTable,
	)

	var record PasswordResetTokenRecord
	var consumedAt *time.Time
	err := h.scylla.Session.Query(query, normalizedEmail).WithContext(ctx).Scan(
		&record.Email,
		&record.OTPHash,
		&record.ExpiresAt,
		&record.CreatedAt,
		&consumedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return PasswordResetTokenRecord{}, false, nil
		}
		return PasswordResetTokenRecord{}, false, err
	}
	record.Consumed = consumedAt != nil && !consumedAt.IsZero()
	record.Email = strings.TrimSpace(strings.ToLower(record.Email))
	record.OTPHash = strings.TrimSpace(record.OTPHash)
	record.ExpiresAt = record.ExpiresAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	return record, true, nil
}

func (h *AuthHandler) deletePasswordResetToken(ctx context.Context, email string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return nil
	}

	resetTable := h.scylla.Table("password_reset_tokens")
	query := fmt.Sprintf(`DELETE FROM %s WHERE email = ?`, resetTable)
	return h.scylla.Session.Query(query, normalizedEmail).WithContext(ctx).Exec()
}

func (h *AuthHandler) insertUser(ctx context.Context, user models.User) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(
		`INSERT INTO %s (id, email, password_hash, google_id, username, full_name, avatar_url, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		usersTable,
	)
	return h.scylla.Session.Query(
		query,
		user.ID,
		strings.TrimSpace(strings.ToLower(user.Email)),
		strings.TrimSpace(user.PasswordHash),
		strings.TrimSpace(user.GoogleID),
		normalizeAccountUsername(user.Username),
		strings.TrimSpace(user.FullName),
		strings.TrimSpace(user.AvatarURL),
		user.CreatedAt.UTC(),
	).WithContext(ctx).Exec()
}

func (h *AuthHandler) getUserByEmail(ctx context.Context, email string) (models.User, bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return models.User{}, false, fmt.Errorf("scylla session is not configured")
	}
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return models.User{}, false, nil
	}

	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(
		`SELECT id, email, password_hash, google_id, username, full_name, avatar_url, created_at FROM %s WHERE email = ? LIMIT 1`,
		usersTable,
	)

	var user models.User
	err := h.scylla.Session.Query(query, normalizedEmail).WithContext(ctx).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.GoogleID,
		&user.Username,
		&user.FullName,
		&user.AvatarURL,
		&user.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return models.User{}, false, nil
		}
		return models.User{}, false, err
	}
	return user, true, nil
}

func setAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	secureCookie := shouldUseSecureCookies(r)
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().UTC().Add(7 * 24 * time.Hour),
	})
}

func clearAuthCookie(w http.ResponseWriter, r *http.Request) {
	expiredAt := time.Unix(0, 0).UTC()
	for _, secureCookie := range []bool{false, true} {
		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secureCookie,
			SameSite: http.SameSiteLaxMode,
			Expires:  expiredAt,
			MaxAge:   -1,
		})
	}
}

func writeAuthJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAuthError(w http.ResponseWriter, code int, message string) {
	writeAuthJSON(w, code, map[string]string{"error": message})
}

func normalizeAuthDisplayName(primaryRaw string, fallbackRaw string, email string) string {
	primary := strings.TrimSpace(primaryRaw)
	if primary == "" {
		primary = strings.TrimSpace(fallbackRaw)
	}
	if primary == "" && strings.TrimSpace(email) != "" {
		parts := strings.SplitN(strings.TrimSpace(email), "@", 2)
		if len(parts) > 0 {
			primary = strings.TrimSpace(parts[0])
		}
	}
	if primary == "" {
		primary = "Guest"
	}
	normalized := strings.Join(strings.Fields(primary), " ")
	if len(normalized) > 80 {
		normalized = normalized[:80]
	}
	if strings.TrimSpace(normalized) == "" {
		return "Guest"
	}
	return normalized
}

func normalizeAccountUsername(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var normalized strings.Builder
	normalized.Grow(len(trimmed))
	lastUnderscore := false

	for _, ch := range trimmed {
		switch {
		case ch >= 'a' && ch <= 'z':
			normalized.WriteRune(ch)
			lastUnderscore = false
		case ch >= 'A' && ch <= 'Z':
			normalized.WriteRune(ch + ('a' - 'A'))
			lastUnderscore = false
		case ch >= '0' && ch <= '9':
			normalized.WriteRune(ch)
			lastUnderscore = false
		case ch == '_' || ch == '-' || ch == ' ':
			if normalized.Len() == 0 || lastUnderscore {
				continue
			}
			normalized.WriteByte('_')
			lastUnderscore = true
		}

		if normalized.Len() >= maxAuthUsernameLength {
			break
		}
	}

	candidate := strings.Trim(normalized.String(), "_")
	if len(candidate) > maxAuthUsernameLength {
		candidate = strings.Trim(candidate[:maxAuthUsernameLength], "_")
	}
	return candidate
}

func deriveAuthUsernameBase(fullName string, email string, userID string) string {
	base := normalizeAccountUsername(fullName)
	if base != "" {
		return base
	}

	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail != "" {
		localPart := normalizedEmail
		if parts := strings.SplitN(normalizedEmail, "@", 2); len(parts) > 0 {
			localPart = parts[0]
		}
		base = normalizeAccountUsername(localPart)
		if base != "" {
			return base
		}
	}

	idFragment := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(userID), "-", ""))
	if len(idFragment) >= 6 {
		return normalizeAccountUsername("user_" + idFragment[:6])
	}
	return "user"
}

func buildUsernameCandidate(base string, attempt int) string {
	normalizedBase := normalizeAccountUsername(base)
	if normalizedBase == "" {
		normalizedBase = "user"
	}
	if attempt <= 0 {
		if len(normalizedBase) > maxAuthUsernameLength {
			return strings.Trim(normalizedBase[:maxAuthUsernameLength], "_")
		}
		return normalizedBase
	}

	suffix := fmt.Sprintf("_%d", attempt+1)
	maxBaseLength := maxAuthUsernameLength - len(suffix)
	if maxBaseLength < 1 {
		maxBaseLength = 1
	}

	truncatedBase := normalizedBase
	if len(truncatedBase) > maxBaseLength {
		truncatedBase = strings.Trim(truncatedBase[:maxBaseLength], "_")
	}
	if truncatedBase == "" {
		truncatedBase = "u"
	}
	return strings.Trim(truncatedBase+suffix, "_")
}

func (h *AuthHandler) reserveUsername(
	ctx context.Context,
	username string,
	userID gocql.UUID,
	createdAt time.Time,
) (bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return false, fmt.Errorf("scylla session is not configured")
	}
	normalizedUsername := normalizeAccountUsername(username)
	if normalizedUsername == "" {
		return false, nil
	}

	usernamesTable := h.scylla.Table("users_by_username")
	query := fmt.Sprintf(
		`INSERT INTO %s (username, user_id, created_at) VALUES (?, ?, ?) IF NOT EXISTS`,
		usernamesTable,
	)

	existing := map[string]any{}
	applied, err := h.scylla.Session.Query(
		query,
		normalizedUsername,
		userID,
		createdAt.UTC(),
	).WithContext(ctx).MapScanCAS(existing)
	if err != nil {
		return false, err
	}
	if !applied {
		if existingUserID, found := extractCASUserID(existing); found && existingUserID == userID {
			return true, nil
		}
	}
	return applied, nil
}

func (h *AuthHandler) releaseUsername(ctx context.Context, username string, userID gocql.UUID) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedUsername := normalizeAccountUsername(username)
	if normalizedUsername == "" {
		return nil
	}

	usernamesTable := h.scylla.Table("users_by_username")
	query := fmt.Sprintf(`DELETE FROM %s WHERE username = ? IF user_id = ?`, usernamesTable)

	existing := map[string]any{}
	_, err := h.scylla.Session.Query(
		query,
		normalizedUsername,
		userID,
	).WithContext(ctx).MapScanCAS(existing)
	return err
}

func extractCASUserID(raw map[string]any) (gocql.UUID, bool) {
	if len(raw) == 0 {
		return gocql.UUID{}, false
	}
	value, exists := raw["user_id"]
	if !exists || value == nil {
		return gocql.UUID{}, false
	}

	switch typed := value.(type) {
	case gocql.UUID:
		return typed, true
	case string:
		parsed, err := gocql.ParseUUID(strings.TrimSpace(typed))
		if err != nil {
			return gocql.UUID{}, false
		}
		return parsed, true
	case []byte:
		parsed, err := gocql.UUIDFromBytes(typed)
		if err != nil {
			return gocql.UUID{}, false
		}
		return parsed, true
	default:
		return gocql.UUID{}, false
	}
}

func (h *AuthHandler) updateUserUsername(ctx context.Context, userID gocql.UUID, username string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedUsername := normalizeAccountUsername(username)
	if normalizedUsername == "" {
		return fmt.Errorf("username is required")
	}

	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(`UPDATE %s SET username = ? WHERE id = ?`, usersTable)
	return h.scylla.Session.Query(query, normalizedUsername, userID).WithContext(ctx).Exec()
}

func (h *AuthHandler) updateUserPasswordHash(ctx context.Context, userID gocql.UUID, passwordHash string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	trimmedPasswordHash := strings.TrimSpace(passwordHash)
	if trimmedPasswordHash == "" {
		return fmt.Errorf("password hash is required")
	}

	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(`UPDATE %s SET password_hash = ? WHERE id = ?`, usersTable)
	return h.scylla.Session.Query(query, trimmedPasswordHash, userID).WithContext(ctx).Exec()
}

func (h *AuthHandler) ensureUserHasUsername(ctx context.Context, user models.User) (models.User, error) {
	normalizedExisting := normalizeAccountUsername(user.Username)
	if normalizedExisting != "" {
		reserved, err := h.reserveUsername(ctx, normalizedExisting, user.ID, user.CreatedAt)
		if err != nil {
			return models.User{}, err
		}
		if !reserved {
			return models.User{}, fmt.Errorf("username is already used by another account")
		}
		if normalizedExisting != user.Username {
			if err := h.updateUserUsername(ctx, user.ID, normalizedExisting); err != nil {
				return models.User{}, err
			}
		}
		user.Username = normalizedExisting
		return user, nil
	}

	base := deriveAuthUsernameBase(user.FullName, user.Email, user.ID.String())
	for attempt := 0; attempt < 64; attempt++ {
		candidate := buildUsernameCandidate(base, attempt)
		reserved, err := h.reserveUsername(ctx, candidate, user.ID, user.CreatedAt)
		if err != nil {
			return models.User{}, err
		}
		if !reserved {
			continue
		}
		if err := h.updateUserUsername(ctx, user.ID, candidate); err != nil {
			_ = h.releaseUsername(ctx, candidate, user.ID)
			return models.User{}, err
		}
		user.Username = candidate
		return user, nil
	}
	return models.User{}, fmt.Errorf("failed to assign unique username")
}

func isLikelyEmail(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	_, err := mail.ParseAddress(trimmed)
	return err == nil
}

func normalizeNumericOTP(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var normalized strings.Builder
	normalized.Grow(len(trimmed))
	for _, ch := range trimmed {
		if ch < '0' || ch > '9' {
			continue
		}
		normalized.WriteRune(ch)
		if normalized.Len() >= passwordResetOTPLength {
			break
		}
	}
	return normalized.String()
}

func newNumericOTP(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("otp length must be positive")
	}
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.Grow(length)
	for _, b := range raw {
		builder.WriteByte('0' + (b % 10))
	}
	return builder.String(), nil
}

func loadPasswordResetOTPTTL() time.Duration {
	defaultTTL := time.Duration(defaultPasswordResetOTPTTLMinutes) * time.Minute
	raw := strings.TrimSpace(os.Getenv(passwordResetOTPTTLMinutesEnv))
	if raw == "" {
		return defaultTTL
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil {
		return defaultTTL
	}
	if minutes < minPasswordResetOTPTTLMinutes {
		minutes = minPasswordResetOTPTTLMinutes
	}
	if minutes > maxPasswordResetOTPTTLMinutes {
		minutes = maxPasswordResetOTPTTLMinutes
	}
	return time.Duration(minutes) * time.Minute
}

func shouldExposePasswordResetDebugOTP() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(passwordResetDebugOTPEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func loadPasswordResetSMTPSettings() passwordResetSMTPSettings {
	settings := passwordResetSMTPSettings{
		Host:        strings.TrimSpace(os.Getenv(passwordResetSMTPHostEnv)),
		Port:        parsePasswordResetSMTPPort(strings.TrimSpace(os.Getenv(passwordResetSMTPPortEnv))),
		Username:    strings.TrimSpace(os.Getenv(passwordResetSMTPUsernameEnv)),
		Password:    strings.TrimSpace(os.Getenv(passwordResetSMTPPasswordEnv)),
		FromAddress: strings.TrimSpace(os.Getenv(passwordResetSMTPFromAddressEnv)),
		FromName:    strings.TrimSpace(os.Getenv(passwordResetSMTPFromNameEnv)),
	}
	if settings.Port <= 0 {
		settings.Port = defaultPasswordResetSMTPPort
	}
	return settings
}

func (s passwordResetSMTPSettings) validate() error {
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	if s.Port <= 0 {
		return fmt.Errorf("smtp port is invalid")
	}
	if !isLikelyEmail(s.FromAddress) {
		return fmt.Errorf("smtp from address is not configured")
	}
	return nil
}

func (s passwordResetSMTPSettings) fromHeader() string {
	trimmedName := strings.TrimSpace(s.FromName)
	trimmedAddress := strings.TrimSpace(s.FromAddress)
	if trimmedName == "" {
		return trimmedAddress
	}
	return fmt.Sprintf("%s <%s>", trimmedName, trimmedAddress)
}

func sendPasswordResetEmail(targetEmail string, otp string, expiresAt time.Time) error {
	settings := loadPasswordResetSMTPSettings()
	if err := settings.validate(); err != nil {
		return err
	}
	normalizedTarget := strings.TrimSpace(strings.ToLower(targetEmail))
	if !isLikelyEmail(normalizedTarget) {
		return fmt.Errorf("invalid recipient email")
	}
	if len(otp) != passwordResetOTPLength {
		return fmt.Errorf("invalid otp value")
	}

	expiresAtUTC := expiresAt.UTC()
	body := fmt.Sprintf(
		"Use this OTP to reset your Converse password: %s\n\nThis code expires at %s UTC.\nIf you did not request this, you can ignore this email.",
		otp,
		expiresAtUTC.Format("2006-01-02 15:04:05"),
	)

	headers := []string{
		fmt.Sprintf("From: %s", settings.fromHeader()),
		fmt.Sprintf("To: %s", normalizedTarget),
		fmt.Sprintf("Subject: %s", passwordResetSMTPSubject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}
	message := strings.Join(headers, "\r\n")

	smtpAddress := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	var smtpAuth smtp.Auth
	if settings.Username != "" || settings.Password != "" {
		smtpAuth = smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)
	}
	return smtp.SendMail(smtpAddress, smtpAuth, settings.FromAddress, []string{normalizedTarget}, []byte(message))
}

func parsePasswordResetSMTPPort(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultPasswordResetSMTPPort
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultPasswordResetSMTPPort
	}
	return value
}

func shouldUseSecureCookies(r *http.Request) bool {
	secureEnv := strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_COOKIE_SECURE")))
	switch secureEnv {
	case "1", "true", "yes", "on":
		return true
	}

	if isLocalRequestHost(r) {
		return false
	}
	return true
}

func isLocalRequestHost(r *http.Request) bool {
	if r == nil {
		return false
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return false
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil && strings.TrimSpace(parsedHost) != "" {
		host = parsedHost
	}
	normalizedHost := strings.Trim(strings.ToLower(host), "[]")
	return normalizedHost == "localhost" ||
		strings.HasSuffix(normalizedHost, ".localhost") ||
		normalizedHost == "127.0.0.1" ||
		normalizedHost == "::1"
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
