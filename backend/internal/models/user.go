package models

import (
	"encoding/json"
	"time"

	"github.com/gocql/gocql"
)

type User struct {
	ID           gocql.UUID `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	GoogleID     string     `json:"googleId,omitempty"`
	Username     string     `json:"username,omitempty"`
	FullName     string     `json:"fullName"`
	AvatarURL    string     `json:"avatarUrl,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	// Tier is not stored in the database; it is resolved at response time
	// based on server configuration (e.g. TEST_USER env var) and injected
	// into the auth response so the frontend can display the correct plan badge.
	Tier string `json:"tier,omitempty"`
}

type userJSON struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	GoogleID  string    `json:"googleId,omitempty"`
	Username  string    `json:"username,omitempty"`
	FullName  string    `json:"fullName"`
	AvatarURL string    `json:"avatarUrl,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Tier      string    `json:"tier,omitempty"`
}

func (u User) MarshalJSON() ([]byte, error) {
	return json.Marshal(userJSON{
		ID:        u.ID.String(),
		Email:     u.Email,
		GoogleID:  u.GoogleID,
		Username:  u.Username,
		FullName:  u.FullName,
		AvatarURL: u.AvatarURL,
		CreatedAt: u.CreatedAt,
		Tier:      u.Tier,
	})
}
