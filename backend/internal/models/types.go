package models

import "time"

const MaxRoomMembers = 1200

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Message struct {
	ID               string     `json:"id"` 
	RoomID           string     `json:"roomId"`
	SenderID         string     `json:"senderId"`
	SenderName       string     `json:"senderName"`
	Content          string     `json:"content"`
	Type             string     `json:"type"`
	MediaURL         string     `json:"mediaUrl,omitempty"`
	MediaType        string     `json:"mediaType,omitempty"`
	FileName         string     `json:"fileName,omitempty"`
	IsEdited         bool       `json:"isEdited,omitempty"`
	EditedAt         *time.Time `json:"editedAt,omitempty"`
	ReplyToMessageID string     `json:"replyToMessageId,omitempty"`
	ReplyToSnippet   string     `json:"replyToSnippet,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	HasBreakRoom     bool       `json:"hasBreakRoom"`
	BreakRoomID      string     `json:"breakRoomId,omitempty"`
	BreakJoinCount   int        `json:"breakJoinCount"`
}

type Room struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Participants    []string  `json:"participants"`
	CreatedAt       time.Time `json:"createdAt"`
	ParentRoomID    string    `json:"parentRoomId,omitempty"`
	OriginMessageID string    `json:"originMessageId,omitempty"`
	MemberCount     int       `json:"memberCount"`
}
