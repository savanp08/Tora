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
	ID               string              `json:"id"`
	RoomID           string              `json:"roomId"`
	SenderID         string              `json:"senderId"`
	SenderName       string              `json:"senderName"`
	Content          string              `json:"content"`
	Type             string              `json:"type"`
	Reactions        map[string][]string `json:"reactions,omitempty"`
	MediaURL         string              `json:"mediaUrl,omitempty"`
	MediaType        string              `json:"mediaType,omitempty"`
	FileName         string              `json:"fileName,omitempty"`
	IsEdited         bool                `json:"isEdited,omitempty"`
	EditedAt         *time.Time          `json:"editedAt,omitempty"`
	ReplyToMessageID string              `json:"replyToMessageId,omitempty"`
	ReplyToSnippet   string              `json:"replyToSnippet,omitempty"`
	IsPinned         bool                `json:"isPinned,omitempty"`
	PinnedBy         string              `json:"pinnedBy,omitempty"`
	PinnedByName     string              `json:"pinnedByName,omitempty"`
	CreatedAt        time.Time           `json:"createdAt"`
	HasBreakRoom     bool                `json:"hasBreakRoom"`
	BreakRoomID      string              `json:"breakRoomId,omitempty"`
	BreakJoinCount   int                 `json:"breakJoinCount"`
}

type Room struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Participants    []string  `json:"participants"`
	CreatedAt       time.Time `json:"createdAt"`
	ParentRoomID    string    `json:"parentRoomId,omitempty"`
	OriginMessageID string    `json:"originMessageId,omitempty"`
	MemberCount     int       `json:"memberCount"`
	AdminCode       string    `json:"adminCode,omitempty"`
}

type BoardElement struct {
	RoomID          string    `json:"roomId"`
	ElementID       string    `json:"elementId"`
	Type            string    `json:"type"`
	X               float32   `json:"x"`
	Y               float32   `json:"y"`
	Width           float32   `json:"width"`
	Height          float32   `json:"height"`
	Content         string    `json:"content"`
	ZIndex          int       `json:"zIndex"`
	CreatedByUserID string    `json:"createdByUserId,omitempty"`
	CreatedByName   string    `json:"createdByName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}
