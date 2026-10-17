package stream

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGroupLifecycleRefreshesLeaseAndRemovesOwnedGroup(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	require.NoError(t, client.XGroupCreateMkStream(ctx, "events", "realtime-gateway-a", "$").Err())

	lifecycle := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	require.NoError(t, lifecycle.RefreshLease(ctx))
	require.Equal(t, int64(1), client.Exists(ctx, "realtime:stream-group:lease:events:realtime-gateway-a").Val())

	require.NoError(t, lifecycle.Close(ctx))
	require.Equal(t, int64(0), client.Exists(ctx, "realtime:stream-group:lease:events:realtime-gateway-a").Val())
	groups, err := client.XInfoGroups(ctx, "events").Result()
	require.NoError(t, err)
	require.Empty(t, groups)
}

func TestGroupLifecycleCloseRemovesLeaseBeforeStreamExists(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	lifecycle := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	require.NoError(t, lifecycle.RefreshLease(ctx))

	require.NoError(t, lifecycle.Close(ctx))

	require.Zero(t, client.Exists(ctx, "realtime:stream-group:lease:events:realtime-gateway-a").Val())
}

func TestGroupLifecycleCleansOnlyStaleRealtimeGroups(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	for _, group := range []string{"realtime-active", "realtime-stale", "message-workers"} {
		require.NoError(t, client.XGroupCreateMkStream(ctx, "events", group, "$").Err())
	}
	active := NewGroupLifecycle(client, "events", "realtime-active", "realtime")
	require.NoError(t, active.RefreshLease(ctx))

	require.NoError(t, active.CleanupStale(ctx))

	groups, err := client.XInfoGroups(ctx, "events").Result()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"realtime-active", "message-workers"}, groupNames(groups))
}

func TestGroupLifecycleDoesNotOverwriteAnotherOwnerLease(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	first := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	second := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	require.NoError(t, first.RefreshLease(ctx))
	require.ErrorIs(t, second.RefreshLease(ctx), ErrGroupLeaseOwned)

	server.FastForward(4 * time.Minute)
	require.ErrorIs(t, second.RefreshLease(ctx), ErrGroupLeaseOwned)
	server.FastForward(2 * time.Minute)
	require.NoError(t, second.RefreshLease(ctx))
}

func TestGroupLifecycleResetsGroupOnlyForCurrentLeaseOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	require.NoError(t, client.XGroupCreateMkStream(ctx, "events", "realtime-gateway-a", "0").Err())

	owner := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	nonOwner := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	require.NoError(t, owner.RefreshLease(ctx))
	require.ErrorIs(t, nonOwner.ResetGroup(ctx), ErrGroupLeaseOwned)

	require.NoError(t, owner.ResetGroup(ctx))
	require.Equal(t, int64(1), client.Exists(ctx, "realtime:stream-group:lease:events:realtime-gateway-a").Val())
	groups, err := client.XInfoGroups(ctx, "events").Result()
	require.NoError(t, err)
	require.Empty(t, groups)
}

func TestGroupLifecycleResetAllowsFirstStartupWithoutStream(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	lifecycle := NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")
	require.NoError(t, lifecycle.RefreshLease(ctx))
	require.NoError(t, lifecycle.ResetGroup(ctx))
}

func TestGroupCleanupLockRefreshesOnlyCurrentOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	const key = "realtime:stream-group:cleanup:events:realtime"
	require.NoError(t, client.Set(ctx, key, "current-owner", time.Second).Err())

	refreshed, err := refreshGroupCleanupLockScript.Run(ctx, client, []string{key}, "old-owner", groupCleanupLockTTL.Milliseconds()).Int64()
	require.NoError(t, err)
	require.Zero(t, refreshed)

	refreshed, err = refreshGroupCleanupLockScript.Run(ctx, client, []string{key}, "current-owner", groupCleanupLockTTL.Milliseconds()).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(1), refreshed)
	require.Greater(t, client.PTTL(ctx, key).Val(), time.Second)
}

func groupNames(groups []redis.XInfoGroup) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}
