package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"time"

	"github.com/google/uuid"
)

type UserReader interface {
	SearchUsers(context.Context, uuid.UUID, string, string, int, *uuid.UUID) ([]*ent.User, string, bool, error)
	GetBlockedUsers(context.Context, uuid.UUID, string, string, int) ([]*ent.User, string, bool, error)
}

type SessionStore interface {
	helper.SessionRevoker
	BlacklistToken(context.Context, string, time.Duration) error
	RevokeAllSessions(context.Context, uuid.UUID) error
	IsUserRevoked(context.Context, uuid.UUID, int64) (bool, error)
}

type TokenBlacklistStore interface {
	IsTokenBlacklisted(context.Context, string) (bool, error)
}

type RateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error)
}

type ChatReader interface {
	GetChatByID(context.Context, uuid.UUID, uuid.UUID) (*ent.Chat, error)
	GetChats(context.Context, uuid.UUID, string, string, int) ([]*ent.Chat, string, bool, error)
}

type GroupMemberReader interface {
	CountActiveMembersByGroupIDs(context.Context, ...uuid.UUID) (map[uuid.UUID]int, error)
	SearchGroupMembers(context.Context, uuid.UUID, string, string, int) ([]*ent.GroupMember, string, bool, error)
}

type GroupChatReader interface {
	SearchPublicGroups(context.Context, string, string, int, string) ([]*ent.GroupChat, string, bool, error)
}

type MessageReader interface {
	GetMessages(context.Context, uuid.UUID, *time.Time, uuid.UUID, int, string) ([]*ent.Message, error)
	GetMessagesAround(context.Context, uuid.UUID, *time.Time, uuid.UUID, int) ([]*ent.Message, error)
}

type MessageAroundPageReader interface {
	GetMessagesAroundPage(context.Context, uuid.UUID, *time.Time, uuid.UUID, int) (*MessageAroundPage, error)
}
