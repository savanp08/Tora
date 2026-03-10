package models

import (
	"time"

	"github.com/gocql/gocql"
)

const MaxRoomMembers = 1200

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
	IsDirect        bool      `json:"is_direct"`
	AdminCode       string    `json:"adminCode,omitempty"`
}

type UserConnection struct {
	UserID    gocql.UUID `json:"user_id"`
	TargetID  gocql.UUID `json:"target_id"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
}

type PersonalItem struct {
	UserID      gocql.UUID `json:"user_id"`
	ItemID      gocql.UUID `json:"item_id"`
	Type        string     `json:"type"`
	Title       string     `json:"title,omitempty"`
	Content     string     `json:"content"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	StartAt     *time.Time `json:"start_at,omitempty"`
	EndAt       *time.Time `json:"end_at,omitempty"`
	RemindAt    *time.Time `json:"remind_at,omitempty"`
	RepeatRule  string     `json:"repeat_rule,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Task struct {
	RoomID          gocql.UUID  `json:"room_id"`
	ID              gocql.UUID  `json:"id"`
	Title           string      `json:"title"`
	Description     string      `json:"description"`
	Status          string      `json:"status"`
	SprintName      string      `json:"sprint_name,omitempty"`
	AssigneeID      *gocql.UUID `json:"assignee_id,omitempty"`
	StatusActorID   string      `json:"status_actor_id,omitempty"`
	StatusActorName string      `json:"status_actor_name,omitempty"`
	StatusChangedAt *time.Time  `json:"status_changed_at,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
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
