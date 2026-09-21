package auth

import (
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestJWTVerifierAcceptsActiveUserAndRejectsRevokedOrBannedUsers(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:websocket-auth?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	ctx := context.Background()
	secret := "websocket-test-secret"
	active := client.User.Create().SetUsername("active").SaveX(ctx)
	token, err := helper.GenerateJWT(secret, 3600, active.ID)
	require.NoError(t, err)

	verifier := NewJWTVerifier(client, redisClient, secret)
	verifiedID, err := verifier.Verify(ctx, token)
	require.NoError(t, err)
	require.Equal(t, active.ID, verifiedID)

	require.NoError(t, redisClient.Set(ctx, "blacklist:"+token, "1", time.Minute).Err())
	_, err = verifier.Verify(ctx, token)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NoError(t, redisClient.Del(ctx, "blacklist:"+token).Err())

	client.User.UpdateOneID(active.ID).SetIsBanned(true).SaveX(ctx)
	_, err = verifier.Verify(ctx, token)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestJWTVerifierRejectsInvalidClaimsAndMissingDependencies(t *testing.T) {
	ctx := context.Background()
	verifier := NewJWTVerifier(nil, nil, "secret")

	_, err := verifier.Verify(ctx, "invalid")
	require.ErrorIs(t, err, ErrUnauthorized)

	token, err := helper.GenerateJWT("secret", 3600, uuid.New())
	require.NoError(t, err)
	_, err = verifier.Verify(ctx, token)
	require.ErrorIs(t, err, ErrUnauthorized)
}
