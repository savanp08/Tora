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
}

type userJSON struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	GoogleID  string    `json:"googleId,omitempty"`
	Username  string    `json:"username,omitempty"`
	FullName  string    `json:"fullName"`
	AvatarURL string    `json:"avatarUrl,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
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
	})
}
