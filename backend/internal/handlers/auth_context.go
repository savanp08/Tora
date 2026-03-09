package handlers

import (
	"context"
	"strings"
)

type authContextKey string

const authUserIDContextKey authContextKey = "auth_user_id"

func WithAuthUserID(ctx context.Context, userID string) context.Context {
	normalizedUserID := strings.TrimSpace(userID)
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, authUserIDContextKey, normalizedUserID)
}

func AuthUserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	raw := ctx.Value(authUserIDContextKey)
	if raw == nil {
		return ""
	}
	userID, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(userID)
}
