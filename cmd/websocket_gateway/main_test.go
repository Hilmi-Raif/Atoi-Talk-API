package main

import (
	"AtoiTalkAPI/internal/websocket_gateway/connection"
	"AtoiTalkAPI/internal/websocket_gateway/stream"
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestInitializeRealtimeStreamReleasesLeaseWhenEnsureFails(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	lifecycle := stream.NewGroupLifecycle(client, "events", "realtime-gateway-a", "realtime")

	err := initializeRealtimeStream(context.Background(), lifecycle, func(context.Context) error {
		return errors.New("create group failed")
	})

	require.ErrorContains(t, err, "create group failed")
	require.Zero(t, client.Exists(context.Background(), "realtime:stream-group:lease:events:realtime-gateway-a").Val())
}

func TestCloseRealtimeClientsUsesIndependentDeadline(t *testing.T) {
	gateway := connection.NewGateway()
	closed := make(chan struct{})
	gateway.RegisterWithClose(uuid.New(), 1, func() { close(closed) })

	expiredCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, expiredCtx.Err())

	closeRealtimeClients(gateway)

	select {
	case <-closed:
	default:
		t.Fatal("expected registered realtime client to be closed")
	}
}
