package auth

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrUnauthorized = fmt.Errorf("unauthorized websocket token")

type JWTVerifier struct {
	client    *ent.Client
	redis     redis.UniversalClient
	jwtSecret string
}

func NewJWTVerifier(client *ent.Client, redisClient redis.UniversalClient, jwtSecret string) *JWTVerifier {
	return &JWTVerifier{client: client, redis: redisClient, jwtSecret: jwtSecret}
}

func (v *JWTVerifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(tokenString, &helper.JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(v.jwtSecret), nil
	})
	if err != nil || token == nil || !token.Valid {
		return uuid.Nil, ErrUnauthorized
	}
	claims, ok := token.Claims.(*helper.JWTClaims)
	if !ok || claims.UserID == uuid.Nil || claims.IssuedAt == nil {
		return uuid.Nil, ErrUnauthorized
	}
	if v.redis != nil {
		blacklisted, err := v.redis.Get(ctx, "blacklist:"+tokenString).Result()
		if err != nil && err != redis.Nil {
			return uuid.Nil, err
		}
		if blacklisted != "" {
			return uuid.Nil, ErrUnauthorized
		}
	}
	if v.client == nil {
		return uuid.Nil, ErrUnauthorized
	}
	u, err := v.client.User.Query().Where(user.ID(claims.UserID), user.DeletedAtIsNil()).Select(user.FieldID, user.FieldIsBanned, user.FieldBannedUntil).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if u.IsBanned && (u.BannedUntil == nil || time.Now().Before(*u.BannedUntil)) {
		return uuid.Nil, ErrUnauthorized
	}
	return u.ID, nil
}
