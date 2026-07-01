package model

import (
	"encoding/json"

	"github.com/google/uuid"
)

type SendMessageRequest struct {
	ChatID        uuid.UUID   `json:"chat_id" validate:"required"`
	Content       string      `json:"content" validate:"required_without=AttachmentIDs,max=4000"`
	AttachmentIDs []uuid.UUID `json:"attachment_ids" validate:"omitempty,dive"`
	ReplyToID     *uuid.UUID  `json:"reply_to_id" validate:"omitempty"`
}

type EditMessageRequest struct {
	Content          string      `json:"content" validate:"required_without=AttachmentIDs,max=4000"`
	AttachmentIDs    []uuid.UUID `json:"attachment_ids" validate:"omitempty,dive"`
	HasAttachmentIDs bool        `json:"-"`
}

func (r *EditMessageRequest) UnmarshalJSON(data []byte) error {
	type alias EditMessageRequest
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoded.HasAttachmentIDs = raw["attachment_ids"] != nil
	*r = EditMessageRequest(decoded)
	return nil
}

type GetMessagesRequest struct {
	ChatID          uuid.UUID  `json:"chat_id" validate:"required"`
	Cursor          string     `json:"cursor" validate:"omitempty"`
	AroundMessageID *uuid.UUID `json:"around_message_id" validate:"omitempty"`
	Limit           int        `json:"limit" validate:"omitempty,gt=0,max=50"`
	Direction       string     `json:"direction" validate:"omitempty,oneof=older newer"`
}

type MessageResponse struct {
	ID     uuid.UUID `json:"id"`
	ChatID uuid.UUID `json:"chat_id"`

	// ID of the sender when available.
	// Can be null when sender account is deleted or relation is unavailable.
	SenderID *uuid.UUID `json:"sender_id,omitempty"`

	// Display name of the sender.
	// Can be "Deleted User" when sender account is deleted.
	SenderName string `json:"sender_name,omitempty"`

	// Avatar URL of the sender
	SenderAvatar string `json:"sender_avatar,omitempty"`

	// Role of the sender in the group (owner, admin, member)
	SenderRole string `json:"sender_role,omitempty"`

	Type    string `json:"type"`
	Content string `json:"content,omitempty"`

	// Metadata for system messages (usually empty for regular messages).
	//
	// Common payload shapes by message type:
	//
	//	system_create:
	//	{
	//	  "initial_name": "My Group"
	//	}
	//
	//	system_rename:
	//	{
	//	  "old_name": "Old Group Name",
	//	  "new_name": "New Group Name"
	//	}
	//
	//	system_description:
	//	{
	//	  "old_description": "Old Desc",
	//	  "new_description": "New Desc"
	//	}
	//
	//	system_avatar:
	//	{
	//	  "action": "updated" // or "removed"
	//	}
	//
	//	system_visibility:
	//	{
	//	  "new_visibility": "public" // or "private"
	//	}
	//
	//	system_add / system_kick:
	//	{
	//	  "target_id": "u1...",
	//	  "actor_id": "u2...",
	//	  "target_name": "Alice", // optional enrichment
	//	  "actor_name": "Bob" // optional enrichment
	//	}
	//
	//	system_promote / system_demote:
	//	{
	//	  "target_id": "u1...",
	//	  "actor_id": "u2...",
	//	  "new_role": "admin", // or "member", "owner"
	//	  "action": "ownership_transferred", // optional
	//	  "target_name": "Alice", // optional enrichment
	//	  "actor_name": "Bob" // optional enrichment
	//	}
	//
	// Notes:
	// - target_name and actor_name are enrichment fields added by service layer.
	// - target_id and actor_id can be removed when referenced users are deleted.
	ActionData map[string]interface{} `json:"action_data,omitempty"`

	Attachments []MediaDTO `json:"attachments"`

	// Preview of the message this message is replying to
	ReplyTo *ReplyPreviewDTO `json:"reply_to,omitempty"`

	CreatedAt string  `json:"created_at"`
	DeletedAt *string `json:"deleted_at,omitempty"`
	EditedAt  *string `json:"edited_at,omitempty"`

	// Total number of members in the group, only for group chats
	MemberCount *int `json:"member_count,omitempty"`
}

type ReplyPreviewDTO struct {
	ID         uuid.UUID  `json:"id"`
	SenderID   *uuid.UUID `json:"sender_id,omitempty"`
	SenderName string     `json:"sender_name"`
	Type       string     `json:"type"`
	Content    string     `json:"content,omitempty"`
	// Metadata for system messages, same structure and behavior as MessageResponse.ActionData.
	ActionData map[string]interface{} `json:"action_data,omitempty"`
	DeletedAt  *string                `json:"deleted_at,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}
