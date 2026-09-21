package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/infrastructure/config"
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
)

type Repository struct {
	Chat        *ChatRepository
	User        *UserRepository
	Message     *MessageRepository
	GroupMember *GroupMemberRepository
	GroupChat   *GroupChatRepository
	Session     *SessionRepository
	RateLimit   *RateLimitRepository
}

func NewRepository(client *ent.Client, redisAdapter *redisinfra.RedisAdapter, cfg *config.AppConfig) *Repository {
	return &Repository{
		Chat:        NewChatRepository(client),
		User:        NewUserRepository(client),
		Message:     NewMessageRepository(client),
		GroupMember: NewGroupMemberRepository(client),
		GroupChat:   NewGroupChatRepository(client),
		Session:     NewSessionRepository(redisAdapter, cfg),
		RateLimit:   NewRateLimitRepository(redisAdapter),
	}
}
