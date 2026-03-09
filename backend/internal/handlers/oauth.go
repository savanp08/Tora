package handlers

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	jwtutil "github.com/savanp08/converse/internal/auth"
	"github.com/savanp08/converse/internal/models"

	"github.com/gocql/gocql"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	googleOAuthStateCookieName = "tora_oauth_state"
	googleOAuthStateTTL        = 5 * time.Minute
	googleUserInfoURL          = "https://www.googleapis.com/oauth2/v2/userinfo"
)

type googleUserProfile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

func (h *AuthHandler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}

	oauthConfig, err := googleOAuthConfig()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Google OAuth is not configured")
		return
	}

	state, err := newOAuthState()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to initialize OAuth flow")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookies(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().UTC().Add(googleOAuthStateTTL),
		MaxAge:   int(googleOAuthStateTTL.Seconds()),
	})

	redirectURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !authLimiter.Allow(clientIP) {
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	oauthConfig, err := googleOAuthConfig()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Google OAuth is not configured")
		return
	}

	stateQuery := strings.TrimSpace(r.URL.Query().Get("state"))
	stateCookie, err := r.Cookie(googleOAuthStateCookieName)
	if err != nil || strings.TrimSpace(stateCookie.Value) == "" || stateQuery == "" || stateCookie.Value != stateQuery {
		writeAuthError(w, http.StatusUnauthorized, "Invalid OAuth state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookies(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeAuthError(w, http.StatusBadRequest, "OAuth code is required")
		return
	}

	token, err := oauthConfig.Exchange(r.Context(), code)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "Failed to exchange OAuth code")
		return
	}

	profile, err := fetchGoogleUserProfile(r.Context(), oauthConfig.Client(r.Context(), token))
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "Failed to fetch Google user profile")
		return
	}

	user, err := h.resolveGoogleOAuthUser(r.Context(), profile)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to resolve OAuth user")
		return
	}

	signedJWT, err := jwtutil.GenerateToken(user.ID.String(), user.Email, user.Username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}
	setAuthCookie(w, r, signedJWT)

	redirectTarget := resolveFrontendDashboardRedirectURL(r)
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

func (h *AuthHandler) resolveGoogleOAuthUser(ctx context.Context, profile googleUserProfile) (models.User, error) {
	googleID := strings.TrimSpace(profile.ID)
	email := strings.TrimSpace(strings.ToLower(profile.Email))
	if googleID == "" || email == "" {
		return models.User{}, fmt.Errorf("google profile is missing id/email")
	}

	if user, exists, err := h.getUserByGoogleID(ctx, googleID); err != nil {
		return models.User{}, err
	} else if exists {
		user, err = h.ensureUserHasUsername(ctx, user)
		if err != nil {
			return models.User{}, err
		}
		return user, nil
	}

	if user, exists, err := h.getUserByEmail(ctx, email); err != nil {
		return models.User{}, err
	} else if exists {
		user.GoogleID = googleID
		user.FullName = normalizeAuthDisplayName(profile.Name, user.FullName, email)
		user.AvatarURL = strings.TrimSpace(profile.Picture)
		if err := h.updateUserGoogleBinding(ctx, user.ID, user.GoogleID, user.FullName, user.AvatarURL); err != nil {
			return models.User{}, err
		}
		user, err = h.ensureUserHasUsername(ctx, user)
		if err != nil {
			return models.User{}, err
		}
		return user, nil
	}

	userID, err := gocql.RandomUUID()
	if err != nil {
		return models.User{}, err
	}
	createdAt := time.Now().UTC()
	baseUsername := deriveAuthUsernameBase(profile.Name, email, userID.String())
	reservedUsername := ""
	for attempt := 0; attempt < 64; attempt++ {
		candidate := buildUsernameCandidate(baseUsername, attempt)
		reserved, reserveErr := h.reserveUsername(ctx, candidate, userID, createdAt)
		if reserveErr != nil {
			return models.User{}, reserveErr
		}
		if reserved {
			reservedUsername = candidate
			break
		}
	}
	if reservedUsername == "" {
		return models.User{}, fmt.Errorf("failed to assign unique username")
	}

	newUser := models.User{
		ID:           userID,
		Email:        email,
		PasswordHash: "",
		GoogleID:     googleID,
		Username:     reservedUsername,
		FullName:     normalizeAuthDisplayName(profile.Name, "", email),
		AvatarURL:    strings.TrimSpace(profile.Picture),
		CreatedAt:    createdAt,
	}
	if err := h.insertUser(ctx, newUser); err != nil {
		_ = h.releaseUsername(ctx, reservedUsername, userID)
		return models.User{}, err
	}
	return newUser, nil
}

func (h *AuthHandler) getUserByGoogleID(ctx context.Context, googleID string) (models.User, bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return models.User{}, false, fmt.Errorf("scylla session is not configured")
	}
	normalizedGoogleID := strings.TrimSpace(googleID)
	if normalizedGoogleID == "" {
		return models.User{}, false, nil
	}

	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(
		`SELECT id, email, password_hash, google_id, username, full_name, avatar_url, created_at FROM %s WHERE google_id = ? LIMIT 1`,
		usersTable,
	)

	var user models.User
	err := h.scylla.Session.Query(query, normalizedGoogleID).WithContext(ctx).Scan(
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

func (h *AuthHandler) updateUserGoogleBinding(
	ctx context.Context,
	userID gocql.UUID,
	googleID string,
	fullName string,
	avatarURL string,
) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	usersTable := h.scylla.Table("users")
	query := fmt.Sprintf(`UPDATE %s SET google_id = ?, full_name = ?, avatar_url = ? WHERE id = ?`, usersTable)
	return h.scylla.Session.Query(
		query,
		strings.TrimSpace(googleID),
		strings.TrimSpace(fullName),
		strings.TrimSpace(avatarURL),
		userID,
	).WithContext(ctx).Exec()
}

func fetchGoogleUserProfile(ctx context.Context, client *http.Client) (googleUserProfile, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return googleUserProfile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return googleUserProfile{}, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return googleUserProfile{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return googleUserProfile{}, fmt.Errorf("google userinfo status %d", resp.StatusCode)
	}

	var profile googleUserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return googleUserProfile{}, err
	}
	return profile, nil
}

func googleOAuthConfig() (*oauth2.Config, error) {
	clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	redirectURL := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil, fmt.Errorf("google oauth env is not configured")
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     google.Endpoint,
		Scopes:       []string{"openid", "profile", "email"},
	}, nil
}

func newOAuthState() (string, error) {
	buf := make([]byte, 24)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func resolveFrontendDashboardRedirectURL(r *http.Request) string {
	base := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if base == "" {
		base = "http://localhost:5173"
	}
	parsed, err := url.Parse(base)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "/dashboard"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/dashboard"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
