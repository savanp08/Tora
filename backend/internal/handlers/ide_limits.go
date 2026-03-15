package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/netutil"
	"github.com/savanp08/converse/internal/security"
)

const (
	ideScopeSession = "session"
	ideScopeIP      = "ip"
	ideScopeDevice  = "device"

	ideActionAI      = "ai"
	ideActionExecute = "execute"

	ideLimitWindowName = "day"
	ideLimitPerDay     = int64(10)
	ideLimitWindow     = 24 * time.Hour

	ideSessionHeader      = "X-Ide-Session-Id"
	ideSessionQueryParam  = "ide_session_id"
	ideDeviceHeader       = "X-Device-Id"
	ideDeviceLegacyHeader = "X-Device-ID"

	ideMaxAIRequestBytes      = int64(256 * 1024)      // 256KB
	ideMaxExecuteRequestBytes = int64(2 * 1024 * 1024) // 2MB
)

type ideLimitExceededError struct {
	Action string
	Scope  string
	Window string
	Limit  int64
}

func (e *ideLimitExceededError) Error() string {
	if e == nil {
		return "IDE rate limit exceeded"
	}
	return fmt.Sprintf(
		"ide rate limit exceeded action=%s scope=%s window=%s limit=%d",
		strings.TrimSpace(e.Action),
		strings.TrimSpace(e.Scope),
		strings.TrimSpace(e.Window),
		e.Limit,
	)
}

func (e *ideLimitExceededError) PublicMessage() string {
	if e == nil {
		return "IDE request limit reached. Please try again later."
	}
	actionLabel := "request"
	switch strings.TrimSpace(e.Action) {
	case ideActionAI:
		actionLabel = "AI request"
	case ideActionExecute:
		actionLabel = "execution request"
	}
	scopeLabel := "this context"
	switch strings.TrimSpace(e.Scope) {
	case ideScopeSession:
		scopeLabel = "this session"
	case ideScopeIP:
		scopeLabel = "this IP"
	case ideScopeDevice:
		scopeLabel = "this device"
	}
	return fmt.Sprintf("IDE %s limit reached for %s (max %d per 24 hours).", actionLabel, scopeLabel, e.Limit)
}

func HandleIDEPrivateAIChat(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !enforceIDEBodySize(w, r, ideMaxAIRequestBytes) {
		return
	}
	if err := enforceIDEActionRateLimits(r, ideActionAI); err != nil {
		var exceeded *ideLimitExceededError
		if errors.As(err, &exceeded) {
			writeIDEError(w, http.StatusTooManyRequests, exceeded.PublicMessage())
			return
		}
		writeIDEError(w, http.StatusServiceUnavailable, "IDE request limiter unavailable")
		return
	}
	HandlePrivateAIChat(w, r)
}

func HandleIDECodeExecution(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !enforceIDEBodySize(w, r, ideMaxExecuteRequestBytes) {
		return
	}
	if err := enforceIDEActionRateLimits(r, ideActionExecute); err != nil {
		var exceeded *ideLimitExceededError
		if errors.As(err, &exceeded) {
			writeIDEError(w, http.StatusTooManyRequests, exceeded.PublicMessage())
			return
		}
		writeIDEError(w, http.StatusServiceUnavailable, "IDE request limiter unavailable")
		return
	}
	HandleCodeExecution(w, r)
}

func enforceIDEActionRateLimits(r *http.Request, action string) error {
	if r == nil {
		return nil
	}
	ctx := r.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	ipAddress := normalizeIDEIPAddress(extractClientIP(r))
	sessionID := extractIDESessionIdentifier(r, ipAddress)
	deviceID := extractIDEDeviceIdentifier(r, sessionID, ipAddress)
	namespace := ideNamespaceForAction(action)
	if namespace == "" {
		return nil
	}

	checks := []struct {
		Scope string
		Value string
	}{
		{Scope: ideScopeIP, Value: ipAddress},
		{Scope: ideScopeDevice, Value: deviceID},
		{Scope: ideScopeSession, Value: sessionID},
	}

	for _, check := range checks {
		if strings.TrimSpace(check.Value) == "" {
			continue
		}
		result, err := security.AllowFixedWindow(
			ctx,
			namespace,
			check.Scope,
			ideLimitWindowName,
			check.Value,
			ideLimitPerDay,
			ideLimitWindow,
		)
		if err != nil {
			return err
		}
		if result.Allowed {
			continue
		}

		monitor.SecurityBlocksTotal.WithLabelValues("ide_" + strings.TrimSpace(action) + "_" + check.Scope + "_limit").Inc()
		return &ideLimitExceededError{
			Action: action,
			Scope:  check.Scope,
			Window: ideLimitWindowName,
			Limit:  ideLimitPerDay,
		}
	}
	return nil
}

func ideNamespaceForAction(action string) string {
	switch strings.TrimSpace(strings.ToLower(action)) {
	case ideActionAI:
		return "ide_ai"
	case ideActionExecute:
		return "ide_execute"
	default:
		return ""
	}
}

func enforceIDEBodySize(w http.ResponseWriter, r *http.Request, maxBytes int64) bool {
	if w == nil || r == nil {
		return false
	}
	if maxBytes <= 0 {
		return true
	}
	if r.ContentLength > maxBytes {
		monitor.SecurityBlocksTotal.WithLabelValues("ide_payload_too_large").Inc()
		writeIDEError(
			w,
			http.StatusRequestEntityTooLarge,
			fmt.Sprintf("Request body exceeds IDE limit (%s).", humanizeByteCount(maxBytes)),
		)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return true
}

func extractIDESessionIdentifier(r *http.Request, ipAddress string) string {
	if r == nil {
		return ""
	}
	sessionID := normalizeIdentifier(firstNonEmpty(
		r.Header.Get(ideSessionHeader),
		r.URL.Query().Get(ideSessionQueryParam),
	))
	if sessionID != "" {
		return sessionID
	}
	ipFallback := normalizeIdentifier(ipAddress)
	if ipFallback == "" {
		ipFallback = "unknown"
	}
	return "missing_session_" + ipFallback
}

func extractIDEDeviceIdentifier(r *http.Request, sessionID string, ipAddress string) string {
	if r == nil {
		return normalizeDeviceIdentifier(sessionID)
	}
	deviceID := normalizeDeviceIdentifier(firstNonEmpty(
		r.Header.Get(ideDeviceHeader),
		r.Header.Get(ideDeviceLegacyHeader),
		r.URL.Query().Get("deviceId"),
		r.URL.Query().Get("device_id"),
	))
	if deviceID != "" {
		return deviceID
	}

	if normalizedSession := normalizeDeviceIdentifier(sessionID); normalizedSession != "" {
		return "missing-device-" + normalizedSession
	}
	if normalizedIP := normalizeDeviceIdentifier(ipAddress); normalizedIP != "" {
		return "missing-device-" + normalizedIP
	}
	return "missing-device-unknown"
}

func normalizeIDEIPAddress(raw string) string {
	normalized := netutil.NormalizeIP(raw)
	if normalized != "" {
		return normalized
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

func writeIDEError(w http.ResponseWriter, status int, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}

func humanizeByteCount(bytes int64) string {
	if bytes <= 0 {
		return "0B"
	}
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + "B"
	}
	mb := float64(bytes) / (1024 * 1024)
	if mb == float64(int64(mb)) {
		return fmt.Sprintf("%dMB", int64(mb))
	}
	return fmt.Sprintf("%.1fMB", mb)
}
