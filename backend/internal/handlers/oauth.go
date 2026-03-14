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
	googleOAuthStateCookieName    = "tora_oauth_state"
	googleOAuthFrontendCookieName = "tora_oauth_frontend"
	googleOAuthStateTTL           = 5 * time.Minute
	googleUserInfoURL             = "https://www.googleapis.com/oauth2/v2/userinfo"
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
	oauthDebugf("Google login started. host=%s path=%s client_ip=%s", strings.TrimSpace(r.Host), r.URL.Path, clientIP)
	if !authLimiter.Allow(clientIP) {
		oauthDebugf("Google login blocked by rate limit. client_ip=%s", clientIP)
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}

	oauthConfig, err := googleOAuthConfig(r)
	if err != nil {
		oauthDebugf("Google login stopped. OAuth config is not valid: %v", err)
		writeAuthError(w, http.StatusServiceUnavailable, "Google OAuth is not configured")
		return
	}
	oauthDebugf(
		"Google login config loaded. redirect_url=%s scopes=%d",
		oauthConfig.RedirectURL,
		len(oauthConfig.Scopes),
	)

	state, err := newOAuthState()
	if err != nil {
		oauthDebugf("Google login stopped. Could not create OAuth state: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "Failed to initialize OAuth flow")
		return
	}
	oauthDebugf("Google login created OAuth state value. state_length=%d", len(state))

	secureCookie := shouldUseSecureCookies(r)
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().UTC().Add(googleOAuthStateTTL),
		MaxAge:   int(googleOAuthStateTTL.Seconds()),
	})
	frontendBase := resolveFrontendBaseURL(r, "").String()
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthFrontendCookieName,
		Value:    url.QueryEscape(frontendBase),
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().UTC().Add(googleOAuthStateTTL),
		MaxAge:   int(googleOAuthStateTTL.Seconds()),
	})
	oauthDebugf(
		"Google login stored OAuth cookies. state_cookie=%s frontend_cookie=%s frontend_base=%s secure_cookie=%t max_age_seconds=%d",
		googleOAuthStateCookieName,
		googleOAuthFrontendCookieName,
		frontendBase,
		secureCookie,
		int(googleOAuthStateTTL.Seconds()),
	)

	redirectURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
	if parsedRedirectURL, parseErr := url.Parse(redirectURL); parseErr == nil {
		oauthDebugf(
			"Google login redirecting user to Google consent page. provider=%s path=%s",
			parsedRedirectURL.Host,
			parsedRedirectURL.Path,
		)
	} else {
		oauthDebugf("Google login redirect URL parsing failed: %v", parseErr)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	stateQuery := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	googleError := strings.TrimSpace(r.URL.Query().Get("error"))
	googleErrorDescription := strings.TrimSpace(r.URL.Query().Get("error_description"))
	oauthDebugf(
		"Google callback received. host=%s path=%s has_state=%t has_code=%t google_error=%q google_error_description=%q client_ip=%s",
		strings.TrimSpace(r.Host),
		r.URL.Path,
		stateQuery != "",
		code != "",
		googleError,
		googleErrorDescription,
		clientIP,
	)
	if !authLimiter.Allow(clientIP) {
		oauthDebugf("Google callback blocked by rate limit. client_ip=%s", clientIP)
		writeAuthError(w, http.StatusTooManyRequests, "Authentication rate limit exceeded")
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		oauthDebugf("Google callback stopped. Authentication storage is unavailable.")
		writeAuthError(w, http.StatusServiceUnavailable, "Authentication storage unavailable")
		return
	}

	oauthConfig, err := googleOAuthConfig(r)
	if err != nil {
		oauthDebugf("Google callback stopped. OAuth config is not valid: %v", err)
		writeAuthError(w, http.StatusServiceUnavailable, "Google OAuth is not configured")
		return
	}
	oauthDebugf("Google callback config loaded. redirect_url=%s", oauthConfig.RedirectURL)

	stateCookie, err := r.Cookie(googleOAuthStateCookieName)
	hasStateCookie := err == nil && strings.TrimSpace(stateCookie.Value) != ""
	stateMatches := hasStateCookie && stateQuery != "" && strings.TrimSpace(stateCookie.Value) == stateQuery
	oauthDebugf(
		"Google callback validating OAuth state. has_state_cookie=%t has_state_query=%t state_matches=%t",
		hasStateCookie,
		stateQuery != "",
		stateMatches,
	)
	if !stateMatches {
		if err != nil {
			oauthDebugf("Google callback state validation failed while reading cookie: %v", err)
		}
		writeAuthError(w, http.StatusUnauthorized, "Invalid OAuth state")
		return
	}
	secureCookie := shouldUseSecureCookies(r)
	frontendBaseOverride := readOAuthFrontendBaseCookie(r)
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthFrontendCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	oauthDebugf(
		"Google callback cleared OAuth cookies. state_cookie=%s frontend_cookie=%s frontend_override_present=%t secure_cookie=%t",
		googleOAuthStateCookieName,
		googleOAuthFrontendCookieName,
		strings.TrimSpace(frontendBaseOverride) != "",
		secureCookie,
	)

	if code == "" {
		oauthDebugf("Google callback stopped. OAuth code is missing in callback query.")
		writeAuthError(w, http.StatusBadRequest, "OAuth code is required")
		return
	}
	oauthDebugf("Google callback exchanging OAuth code for token.")

	token, err := oauthConfig.Exchange(r.Context(), code)
	if err != nil {
		oauthDebugf("Google callback failed while exchanging OAuth code: %v", err)
		writeAuthError(w, http.StatusUnauthorized, "Failed to exchange OAuth code")
		return
	}
	oauthDebugf(
		"Google callback exchanged OAuth code for token successfully. token_type=%s access_token_length=%d",
		strings.TrimSpace(token.TokenType),
		len(strings.TrimSpace(token.AccessToken)),
	)

	oauthDebugf("Google callback fetching Google user profile.")
	profile, err := fetchGoogleUserProfile(r.Context(), oauthConfig.Client(r.Context(), token))
	if err != nil {
		oauthDebugf("Google callback failed while fetching Google profile: %v", err)
		writeAuthError(w, http.StatusUnauthorized, "Failed to fetch Google user profile")
		return
	}
	oauthDebugf(
		"Google callback received Google profile. google_id_present=%t email=%s verified_email=%t",
		strings.TrimSpace(profile.ID) != "",
		maskEmailForDebug(profile.Email),
		profile.VerifiedEmail,
	)

	oauthDebugf("Google callback resolving or creating local user account from Google profile.")
	user, err := h.resolveGoogleOAuthUser(r.Context(), profile)
	if err != nil {
		oauthDebugf("Google callback failed while resolving local account: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "Failed to resolve OAuth user")
		return
	}
	oauthDebugf(
		"Google callback resolved local account successfully. user_id=%s email=%s username=%s",
		strings.TrimSpace(user.ID.String()),
		maskEmailForDebug(user.Email),
		strings.TrimSpace(user.Username),
	)

	signedJWT, err := jwtutil.GenerateToken(user.ID.String(), user.Email, user.Username)
	if err != nil {
		oauthDebugf("Google callback failed while generating JWT token: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "Failed to generate auth token")
		return
	}
	oauthDebugf("Google callback generated JWT token successfully. token_length=%d", len(strings.TrimSpace(signedJWT)))
	setAuthCookie(w, r, signedJWT)
	oauthDebugf("Google callback set auth cookie. secure_cookie=%t host=%s", shouldUseSecureCookies(r), strings.TrimSpace(r.Host))

	redirectTarget := resolveFrontendGoogleSuccessRedirectURL(signedJWT, user, r, frontendBaseOverride)
	oauthDebugf("Google callback redirecting user back to frontend. redirect_target=%s", redirectTarget)
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

func (h *AuthHandler) resolveGoogleOAuthUser(ctx context.Context, profile googleUserProfile) (models.User, error) {
	googleID := strings.TrimSpace(profile.ID)
	email := strings.TrimSpace(strings.ToLower(profile.Email))
	oauthDebugf(
		"Resolving local account from Google profile. google_id_present=%t email=%s verified_email=%t",
		googleID != "",
		maskEmailForDebug(email),
		profile.VerifiedEmail,
	)
	if googleID == "" || email == "" {
		oauthDebugf("Cannot resolve local account. Google profile is missing id or email.")
		return models.User{}, fmt.Errorf("google profile is missing id/email")
	}

	if user, exists, err := h.getUserByGoogleID(ctx, googleID); err != nil {
		oauthDebugf("Failed to read user by Google ID: %v", err)
		return models.User{}, err
	} else if exists {
		oauthDebugf("Found existing local account by Google ID. user_id=%s", user.ID.String())
		user, err = h.ensureUserHasUsername(ctx, user)
		if err != nil {
			oauthDebugf("Failed to ensure username for Google-linked account. user_id=%s err=%v", user.ID.String(), err)
			return models.User{}, err
		}
		oauthDebugf("Returning existing Google-linked local account. user_id=%s username=%s", user.ID.String(), user.Username)
		return user, nil
	}
	oauthDebugf("No existing account found for Google ID. Trying to match by email.")

	if user, exists, err := h.getUserByEmail(ctx, email); err != nil {
		oauthDebugf("Failed to read user by email during Google login: %v", err)
		return models.User{}, err
	} else if exists {
		oauthDebugf("Found existing local account by email. Binding Google ID. user_id=%s", user.ID.String())
		user.GoogleID = googleID
		user.FullName = normalizeAuthDisplayName(profile.Name, user.FullName, email)
		user.AvatarURL = strings.TrimSpace(profile.Picture)
		if err := h.updateUserGoogleBinding(ctx, user.ID, user.GoogleID, user.FullName, user.AvatarURL); err != nil {
			oauthDebugf("Failed to bind Google profile to existing account. user_id=%s err=%v", user.ID.String(), err)
			return models.User{}, err
		}
		user, err = h.ensureUserHasUsername(ctx, user)
		if err != nil {
			oauthDebugf("Failed to ensure username after binding Google profile. user_id=%s err=%v", user.ID.String(), err)
			return models.User{}, err
		}
		oauthDebugf("Bound Google profile to existing account successfully. user_id=%s username=%s", user.ID.String(), user.Username)
		return user, nil
	}
	oauthDebugf("No existing account matched by email. Creating a brand-new local account.")

	userID, err := gocql.RandomUUID()
	if err != nil {
		oauthDebugf("Failed to generate UUID for new OAuth user: %v", err)
		return models.User{}, err
	}
	createdAt := time.Now().UTC()
	baseUsername := deriveAuthUsernameBase(profile.Name, email, userID.String())
	reservedUsername := ""
	for attempt := 0; attempt < 64; attempt++ {
		candidate := buildUsernameCandidate(baseUsername, attempt)
		oauthDebugf("Trying username candidate for new OAuth user. attempt=%d candidate=%s", attempt+1, candidate)
		reserved, reserveErr := h.reserveUsername(ctx, candidate, userID, createdAt)
		if reserveErr != nil {
			oauthDebugf("Failed while reserving username candidate. candidate=%s err=%v", candidate, reserveErr)
			return models.User{}, reserveErr
		}
		if reserved {
			reservedUsername = candidate
			oauthDebugf("Reserved username candidate successfully. candidate=%s", candidate)
			break
		}
	}
	if reservedUsername == "" {
		oauthDebugf("Failed to assign any unique username after all attempts.")
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
		oauthDebugf("Failed to insert new OAuth user in database. user_id=%s err=%v", userID.String(), err)
		_ = h.releaseUsername(ctx, reservedUsername, userID)
		return models.User{}, err
	}
	oauthDebugf("Created new OAuth user successfully. user_id=%s username=%s", userID.String(), reservedUsername)
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
	oauthDebugf("Sending request to Google userinfo endpoint.")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		oauthDebugf("Failed to create Google userinfo request: %v", err)
		return googleUserProfile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		oauthDebugf("Google userinfo request failed: %v", err)
		return googleUserProfile{}, err
	}
	defer resp.Body.Close()
	oauthDebugf("Google userinfo response received. status_code=%d", resp.StatusCode)

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		oauthDebugf("Failed to read Google userinfo response body: %v", readErr)
		return googleUserProfile{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		oauthDebugf("Google userinfo returned non-success status. status_code=%d", resp.StatusCode)
		return googleUserProfile{}, fmt.Errorf("google userinfo status %d", resp.StatusCode)
	}

	var profile googleUserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		oauthDebugf("Failed to decode Google userinfo JSON payload: %v", err)
		return googleUserProfile{}, err
	}
	oauthDebugf(
		"Decoded Google user profile successfully. google_id_present=%t email=%s verified_email=%t",
		strings.TrimSpace(profile.ID) != "",
		maskEmailForDebug(profile.Email),
		profile.VerifiedEmail,
	)
	return profile, nil
}

func googleOAuthConfig(r *http.Request) (*oauth2.Config, error) {
	clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	redirectURL := resolveGoogleOAuthRedirectURL(r)
	if clientID == "" || clientSecret == "" {
		oauthDebugf("Google OAuth config missing required env vars. has_client_id=%t has_client_secret=%t", clientID != "", clientSecret != "")
		return nil, fmt.Errorf("google oauth env is not configured")
	}
	oauthDebugf(
		"Google OAuth config ready. redirect_url=%s has_client_id=%t has_client_secret=%t",
		redirectURL,
		clientID != "",
		clientSecret != "",
	)
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
	parsed := resolveFrontendBaseURL(r, "")
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/dashboard"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	oauthDebugf("Resolved frontend dashboard redirect URL: %s", parsed.String())
	return parsed.String()
}

func resolveFrontendGoogleSuccessRedirectURL(token string, user models.User, r *http.Request, frontendBaseOverride string) string {
	parsed := resolveFrontendBaseURL(r, frontendBaseOverride)
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/login"
	parsed.RawQuery = ""

	fragment := url.Values{}
	fragment.Set("oauth_token", strings.TrimSpace(token))
	fragment.Set("oauth_user_id", strings.TrimSpace(user.ID.String()))
	fragment.Set("oauth_email", strings.TrimSpace(strings.ToLower(user.Email)))
	fragment.Set("oauth_username", strings.TrimSpace(user.Username))
	fragment.Set("oauth_full_name", strings.TrimSpace(user.FullName))
	fragment.Set("oauth_avatar_url", strings.TrimSpace(user.AvatarURL))
	parsed.Fragment = fragment.Encode()
	oauthDebugf(
		"Resolved frontend OAuth success redirect URL. path=%s fragment_has_token=%t user_id=%s",
		parsed.Path,
		strings.TrimSpace(token) != "",
		strings.TrimSpace(user.ID.String()),
	)
	return parsed.String()
}

func resolveFrontendBaseURL(r *http.Request, frontendBaseOverride string) *url.URL {
	if configured := parseAbsoluteHTTPURL(strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))); configured != nil {
		oauthDebugf("Frontend base URL resolved from FRONTEND_BASE_URL: %s", configured.String())
		return configured
	}
	if override := parseAbsoluteHTTPURL(frontendBaseOverride); override != nil {
		oauthDebugf("Frontend base URL resolved from OAuth cookie override: %s", override.String())
		return override
	}
	if r != nil {
		if requestOrigin := parseAbsoluteHTTPURL(firstHeaderValue(r.Header.Get("Origin"))); requestOrigin != nil {
			oauthDebugf("Frontend base URL resolved from request Origin header: %s", requestOrigin.String())
			return requestOrigin
		}
	}
	if refererOrigin := parseRefererOrigin(r); refererOrigin != nil {
		oauthDebugf("Frontend base URL resolved from request Referer header: %s", refererOrigin.String())
		return refererOrigin
	}

	fallback := resolveRequestBaseURL(r)
	if fallback != nil {
		oauthDebugf("Frontend base URL resolved from request host fallback: %s", fallback.String())
		return fallback
	}

	localhostFallback, _ := url.Parse("http://localhost:5173")
	oauthDebugf("Frontend base URL fallback default used: %s", localhostFallback.String())
	return localhostFallback
}

func resolveGoogleOAuthRedirectURL(r *http.Request) string {
	configured := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL"))
	if parsed := parseAbsoluteHTTPURL(configured); parsed != nil {
		parsed.Path = "/api/auth/google/callback"
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}

	if strings.TrimSpace(configured) != "" {
		oauthDebugf("OAUTH_REDIRECT_URL is invalid. Falling back to request host. value=%q", configured)
	}

	fallback := resolveRequestBaseURL(r)
	if fallback == nil {
		fallback, _ = url.Parse("http://localhost:8080")
	}
	fallback.Path = "/api/auth/google/callback"
	fallback.RawQuery = ""
	fallback.Fragment = ""
	return fallback.String()
}

func readOAuthFrontendBaseCookie(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(googleOAuthFrontendCookieName)
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(cookie.Value)
	if raw == "" {
		return ""
	}
	decoded, decodeErr := url.QueryUnescape(raw)
	if decodeErr != nil {
		return ""
	}
	parsed := parseAbsoluteHTTPURL(decoded)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

func resolveRequestBaseURL(r *http.Request) *url.URL {
	host := resolveRequestHost(r)
	if host == "" {
		return nil
	}
	return &url.URL{
		Scheme: resolveRequestScheme(r),
		Host:   host,
	}
}

func resolveRequestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	candidates := []string{
		firstHeaderValue(r.Header.Get("X-Forwarded-Host")),
		strings.TrimSpace(r.Host),
	}
	for _, candidate := range candidates {
		normalized := strings.TrimSpace(candidate)
		if normalized == "" {
			continue
		}
		if strings.ContainsAny(normalized, " \t\r\n/\\") {
			continue
		}
		return normalized
	}
	return ""
}

func resolveRequestScheme(r *http.Request) string {
	if r == nil {
		return "http"
	}
	forwarded := strings.ToLower(strings.TrimSpace(firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))))
	if forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func firstHeaderValue(raw string) string {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func parseAbsoluteHTTPURL(raw string) *url.URL {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return nil
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed
}

func parseRefererOrigin(r *http.Request) *url.URL {
	if r == nil {
		return nil
	}
	referer := strings.TrimSpace(r.Referer())
	parsed := parseAbsoluteHTTPURL(referer)
	if parsed == nil {
		return nil
	}
	return &url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
	}
}

func oauthDebugf(_ string, _ ...any) {
	// Google OAuth debug logs intentionally disabled.
}

func maskEmailForDebug(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	parts := strings.SplitN(trimmed, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local := strings.TrimSpace(parts[0])
	domain := strings.TrimSpace(parts[1])
	if local == "" || domain == "" {
		return "***"
	}
	if len(local) <= 2 {
		return local[:1] + "***@" + domain
	}
	return local[:1] + "***" + local[len(local)-1:] + "@" + domain
}
