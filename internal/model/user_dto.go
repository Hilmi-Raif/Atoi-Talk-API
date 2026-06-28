package model

import "github.com/google/uuid"

type UserDTO struct {
	ID               uuid.UUID  `json:"id"`
	Email            string     `json:"email,omitempty"`
	Username         string     `json:"username"`
	FullName         string     `json:"full_name"`
	Avatar           string     `json:"avatar"`
	Bio              string     `json:"bio,omitempty"`
	Role             string     `json:"role"`
	HasPassword      bool       `json:"has_password,omitempty"`
	PrivateChatID    *uuid.UUID `json:"private_chat_id,omitempty"`
	IsBlockedByMe    *bool      `json:"is_blocked_by_me,omitempty"`
	IsBlockedByOther *bool      `json:"is_blocked_by_other,omitempty"`
	IsOnline         *bool      `json:"is_online,omitempty"`
	IsBanned         *bool      `json:"is_banned,omitempty"`
	LastSeenAt       *string    `json:"last_seen_at,omitempty"`
}

type UserUpdateEventPayload struct {
	ID         uuid.UUID `json:"id"`
	Username   string    `json:"username"`
	FullName   string    `json:"full_name"`
	Avatar     string    `json:"avatar"`
	Bio        string    `json:"bio"`
	LastSeenAt *string   `json:"last_seen_at,omitempty"`
}

type CreateUserDTO struct {
	Email    string
	Username string
	FullName string
	Avatar   string
}

type UpdateProfileRequest struct {
	Username      string     `json:"username" validate:"omitempty,min=3,max=50,alphanum"`
	FullName      string     `json:"full_name" validate:"required,min=3,max=100"`
	Bio           string     `json:"bio" validate:"max=255"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id" validate:"omitempty"`
	DeleteAvatar  bool       `json:"delete_avatar"`
}

type SearchUserRequest struct {
	Query         string     `json:"query" validate:"omitempty,min=1,max=100"`
	Cursor        string     `json:"cursor"`
	Limit         int        `json:"limit" validate:"min=1,max=50"`
	IncludeChatID bool       `json:"include_chat_id"`
	ExcludeChatID *uuid.UUID `json:"exclude_chat_id"`
}

type GetBlockedUsersRequest struct {
	Query  string `json:"query" validate:"omitempty,min=1,max=100"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit" validate:"min=1,max=50"`
}
