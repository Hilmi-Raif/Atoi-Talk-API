package model

import (
	"github.com/google/uuid"
)

type CreatePrivateChatRequest struct {
	TargetUserID uuid.UUID `json:"target_user_id" validate:"required"`
}

type ChatResponse struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	CreatedAt string    `json:"created_at"`
}

type ChatListResponse struct {
	ID   uuid.UUID `json:"id"`
	Type string    `json:"type"`

	// Common fields

	// Name of the group or the other user in a private chat
	Name string `json:"name"`

	// Avatar URL of the group or the other user
	Avatar string `json:"avatar"`

	// Preview of the last message in the chat
	LastMessage *MessageResponse `json:"last_message"`

	// Number of unread messages for the current user
	UnreadCount int `json:"unread_count"`

	// Timestamp when the current user last read the chat
	LastReadAt *string `json:"last_read_at,omitempty"`

	// Timestamp when the current user hid the chat
	HiddenAt *string `json:"hidden_at,omitempty"`

	// Indicates if the current user has blocked the other user
	IsBlockedByMe bool `json:"is_blocked_by_me"`

	// Private Chat specific fields

	// ID of the other user in a private chat
	OtherUserID *uuid.UUID `json:"other_user_id,omitempty"`

	// Indicates if the other user's account has been deleted
	OtherUserIsDeleted bool `json:"other_user_is_deleted"`

	// Indicates if the other user is currently banned
	OtherUserIsBanned bool `json:"other_user_is_banned"`

	// Indicates if the other user has blocked the current user
	IsBlockedByOther bool `json:"is_blocked_by_other"`

	// Indicates if the other user is currently online
	IsOnline bool `json:"is_online"`

	// Timestamp when the other user last read the chat
	OtherLastReadAt *string `json:"other_last_read_at,omitempty"`

	// Group Chat specific fields

	// Description of the group
	Description *string `json:"description,omitempty"`

	// Indicates if the group is public
	IsPublic *bool `json:"is_public,omitempty"`

	// Invite code for the group, available if public or for admins
	InviteCode *string `json:"invite_code,omitempty"`

	// Expiration timestamp for the invite code
	InviteExpiresAt *string `json:"invite_expires_at,omitempty"`

	// Role of the current user in the group (owner, admin, member)
	MyRole *string `json:"my_role,omitempty"`

	// Total number of members in the group
	MemberCount int `json:"member_count"`
}

type GetChatsRequest struct {
	Query  string `json:"query" validate:"omitempty,max=100"`
	Cursor string `json:"cursor" validate:"omitempty"`
	Limit  int    `json:"limit" validate:"omitempty,gt=0,max=50"`
}
