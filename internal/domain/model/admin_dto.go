package model

import (
	"github.com/google/uuid"
)

type CreateReportRequest struct {
	TargetType  string     `json:"target_type" validate:"required,oneof=message group user"`
	Reason      string     `json:"reason" validate:"required"`
	Description string     `json:"description"`
	MessageID   *uuid.UUID `json:"message_id,omitempty"`
	// Chat entity UUID of the group to report. Required when target_type is "group".
	// This is the ID visible in chat lists, not the internal GroupChat entity ID.
	ChatID *uuid.UUID `json:"chat_id,omitempty"`

	TargetUserID *uuid.UUID `json:"target_user_id,omitempty"`
}

type BanUserRequest struct {
	TargetUserID  uuid.UUID `json:"target_user_id" validate:"required"`
	Reason        string    `json:"reason" validate:"required"`
	DurationHours int       `json:"duration_hours" validate:"omitempty,min=0"`
}

type ResolveReportRequest struct {
	Status string `json:"status" validate:"required,oneof=resolved rejected"`
	Notes  string `json:"notes"`
}

type ReportListResponse struct {
	ID           uuid.UUID `json:"id"`
	TargetType   string    `json:"target_type"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	ReporterName string    `json:"reporter_name"`
	CreatedAt    string    `json:"created_at"`
}

type ReportDetailResponse struct {
	ID         uuid.UUID `json:"id"`
	TargetType string    `json:"target_type"`

	// ID of the reported entity.
	// For target_type "user" this is the user UUID.
	// For target_type "message" this is the message UUID.
	// For target_type "group" this is the internal GroupChat UUID (not the chat_id).
	// To navigate to the group in the frontend, use evidence_snapshot.chat_id instead.
	TargetID *uuid.UUID `json:"target_id,omitempty"`

	// Indicates if the target entity has been soft-deleted
	TargetIsDeleted bool `json:"target_is_deleted"`

	// Indicates if the target user (or sender of the message) is banned
	TargetIsBanned bool `json:"target_is_banned"`

	Reason      string  `json:"reason"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status"`

	ReporterID     uuid.UUID `json:"reporter_id"`
	ReporterName   string    `json:"reporter_name"`
	ReporterAvatar string    `json:"reporter_avatar"`

	// Indicates if the reporter's account has been soft-deleted
	ReporterIsDeleted bool `json:"reporter_is_deleted"`

	// Indicates if the reporter is banned
	ReporterIsBanned bool `json:"reporter_is_banned"`

	// Snapshot of reported entity data at report creation time.
	// Shape depends on target_type.
	//
	// In shapes below, chat_id is the Chat entity UUID used by frontend.
	// group_id is the internal GroupChat entity UUID for backend reference only.
	//
	// target_type = "message":
	//
	//	{
	//	  "content": "Offensive content...", // can be null
	//	  "sender_id": "u1...", // empty string when sender ID unavailable
	//	  "sender_username": "alice",
	//	  "sender_name": "Alice Wonderland",
	//	  "sent_at": "2026-02-19T21:14:13Z",
	//	  "chat_id": "c1...", // frontend-facing Chat UUID
	//	  "chat_type": "group", // or "private"
	//	  "is_edited": false,
	//	  "attachments": [
	//	    {
	//	      "id": "m1...",
	//	      "file_name": "abc.jpg",
	//	      "original_name": "photo.jpg",
	//	      "file_size": 12345,
	//	      "mime_type": "image/jpeg",
	//	      "url": "https://..."
	//	    }
	//	  ],
	//	  "group_id": "g1..." // internal GroupChat UUID, only when chat_type is "group"
	//	}
	//
	// target_type = "group":
	//
	//	{
	//	  "group_id": "g1...", // internal GroupChat UUID
	//	  "chat_id": "c1...",  // frontend-facing Chat UUID
	//	  "name": "Bad Group",
	//	  "description": "...", // can be null
	//	  "avatar": "https://...", // can be empty string
	//	  "created_by": "u2..." // can be null
	//	}
	//
	// target_type = "user":
	//
	//	{
	//	  "user_id": "u3...",
	//	  "username": "baduser", // can be null
	//	  "full_name": "Bad User", // can be null
	//	  "bio": "...", // can be null
	//	  "avatar": "https://..." // can be empty string
	//	}
	//
	// Note: attachment URLs may be refreshed when admin fetches report detail.
	EvidenceSnapshot map[string]interface{} `json:"evidence_snapshot"`

	AdminNotes *string `json:"admin_notes,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

type GetReportsRequest struct {
	Query  string `json:"query" validate:"omitempty,max=100"`
	Status string `json:"status" validate:"omitempty,oneof=pending reviewed resolved rejected"`
	Limit  int    `json:"limit" validate:"omitempty,gt=0,max=50"`
	Cursor string `json:"cursor" validate:"omitempty"`
}

type DashboardStatsResponse struct {
	TotalUsers    int `json:"total_users"`
	TotalGroups   int `json:"total_groups"`
	TotalMessages int `json:"total_messages"`
	ActiveReports int `json:"active_reports"`
}

type AdminGetUserListRequest struct {
	Query  string `json:"query" validate:"omitempty,max=100"`
	Role   string `json:"role" validate:"omitempty,oneof=user admin"`
	Limit  int    `json:"limit" validate:"omitempty,gt=0,max=50"`
	Cursor string `json:"cursor" validate:"omitempty"`
}

type AdminUserListResponse struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Email     *string   `json:"email"`
	FullName  *string   `json:"full_name"`
	Role      string    `json:"role"`
	IsBanned  bool      `json:"is_banned"`
	CreatedAt string    `json:"created_at"`
}

type AdminUserDetailResponse struct {
	ID            uuid.UUID `json:"id"`
	Username      string    `json:"username"`
	Email         *string   `json:"email"`
	FullName      *string   `json:"full_name"`
	Bio           *string   `json:"bio"`
	Avatar        string    `json:"avatar"`
	Role          string    `json:"role"`
	IsBanned      bool      `json:"is_banned"`
	BanReason     *string   `json:"ban_reason,omitempty"`
	BannedUntil   *string   `json:"banned_until,omitempty"`
	CreatedAt     string    `json:"created_at"`
	LastSeenAt    *string   `json:"last_seen_at"`
	TotalMessages int       `json:"total_messages"`
	TotalGroups   int       `json:"total_groups"`
}

type ResetUserInfoRequest struct {
	TargetUserID uuid.UUID `json:"target_user_id" validate:"required"`
	ResetAvatar  bool      `json:"reset_avatar"`
	ResetBio     bool      `json:"reset_bio"`
	ResetName    bool      `json:"reset_name"`
}

type AdminGetGroupListRequest struct {
	Query  string `json:"query" validate:"omitempty,max=100"`
	Limit  int    `json:"limit" validate:"omitempty,gt=0,max=50"`
	Cursor string `json:"cursor" validate:"omitempty"`
}

type AdminGroupListResponse struct {
	ID          uuid.UUID `json:"id"`
	ChatID      uuid.UUID `json:"chat_id"`
	Name        string    `json:"name"`
	MemberCount int       `json:"member_count"`
	IsPublic    bool      `json:"is_public"`
	CreatedAt   string    `json:"created_at"`
}

type AdminGroupDetailResponse struct {
	ID            uuid.UUID  `json:"id"`
	ChatID        uuid.UUID  `json:"chat_id"`
	Name          string     `json:"name"`
	Description   *string    `json:"description"`
	Avatar        string     `json:"avatar"`
	IsPublic      bool       `json:"is_public"`
	CreatorID     *uuid.UUID `json:"creator_id"`
	CreatorName   *string    `json:"creator_name"`
	MemberCount   int        `json:"member_count"`
	TotalMessages int        `json:"total_messages"`
	CreatedAt     string     `json:"created_at"`
}

type ResetGroupInfoRequest struct {
	ResetAvatar      bool `json:"reset_avatar"`
	ResetDescription bool `json:"reset_description"`
	ResetName        bool `json:"reset_name"`
}
