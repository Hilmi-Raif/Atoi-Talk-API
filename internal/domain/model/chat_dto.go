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
	ID                 uuid.UUID        `json:"id"`
	Type               string           `json:"type"`
	Name               string           `json:"name"`
	Avatar             string           `json:"avatar"`
	LastMessage        *MessageResponse `json:"last_message"`
	UnreadCount        int              `json:"unread_count"`
	LastReadAt         *string          `json:"last_read_at,omitempty"`
	HiddenAt           *string          `json:"hidden_at,omitempty"`
	IsBlockedByMe      bool             `json:"is_blocked_by_me"`
	OtherUserID        *uuid.UUID       `json:"other_user_id,omitempty"`
	OtherUserIsDeleted bool             `json:"other_user_is_deleted"`
	OtherUserIsBanned  bool             `json:"other_user_is_banned"`
	IsBlockedByOther   bool             `json:"is_blocked_by_other"`
	IsOnline           bool             `json:"is_online"`
	OtherLastReadAt    *string          `json:"other_last_read_at,omitempty"`
	Description        *string          `json:"description,omitempty"`
	IsPublic           *bool            `json:"is_public,omitempty"`
	InviteCode         *string          `json:"invite_code,omitempty"`
	InviteExpiresAt    *string          `json:"invite_expires_at,omitempty"`
	MyRole             *string          `json:"my_role,omitempty"`
	MemberCount        int              `json:"member_count"`
}

type GetChatsRequest struct {
	Query  string `json:"query" validate:"omitempty,max=100"`
	Cursor string `json:"cursor" validate:"omitempty"`
	Limit  int    `json:"limit" validate:"omitempty,gt=0,max=50"`
}
