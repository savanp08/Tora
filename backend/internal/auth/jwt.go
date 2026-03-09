package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenTTL = 7 * 24 * time.Hour

type Claims struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Username string `json:"username,omitempty"`
	jwt.RegisteredClaims
}

func GenerateToken(userID string, email string, username string) (string, error) {
	normalizedUserID := strings.TrimSpace(userID)
	if normalizedUserID == "" {
		return "", fmt.Errorf("user id is required")
	}

	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	claims := Claims{
		UserID:   normalizedUserID,
		Email:    strings.TrimSpace(strings.ToLower(email)),
		Username: strings.TrimSpace(username),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   normalizedUserID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func ValidateToken(tokenString string) (*Claims, error) {
	normalizedToken := strings.TrimSpace(tokenString)
	if normalizedToken == "" {
		return nil, fmt.Errorf("token is required")
	}

	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}

	claims := &Claims{}
	parsedToken, err := jwt.ParseWithClaims(normalizedToken, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if parsedToken == nil || !parsedToken.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func jwtSecret() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("APP_SECRET_KEY"))
	if secret == "" {
		return nil, fmt.Errorf("APP_SECRET_KEY is required for JWT signing")
	}
	return []byte(secret), nil
}
