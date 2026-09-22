package connection

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type gatewayPresenceProbe struct {
	connected    chan uuid.UUID
	disconnected chan uuid.UUID
	refreshed    chan uuid.UUID
}

func (p *gatewayPresenceProbe) Connected(_ context.Context, userID uuid.UUID) {
	p.connected <- userID
}

func (p *gatewayPresenceProbe) Disconnected(_ context.Context, userID uuid.UUID) {
	p.disconnected <- userID
}

func (p *gatewayPresenceProbe) Refresh(_ context.Context, userID uuid.UUID) {
	p.refreshed <- userID
}

func TestGatewayRefreshesPresenceForConnectedClient(t *testing.T) {
	probe := &gatewayPresenceProbe{
		connected: make(chan uuid.UUID, 1),
		refreshed: make(chan uuid.UUID, 1),
	}
	gateway := NewGatewayWithPresence(probe)
	userID := uuid.New()
	client := gateway.Register(userID)

	gateway.RefreshPresence(client)

	require.Equal(t, userID, <-probe.refreshed)
}

func TestGatewayDeliversOnlyToConnectionsForTargetUser(t *testing.T) {
	gateway := NewGateway()
	userID := uuid.New()
	otherUserID := uuid.New()
	userClient := gateway.Register(userID)
	otherClient := gateway.Register(otherUserID)

	payload := []byte(`{"type":"message.new"}`)
	require.NoError(t, gateway.Deliver(context.Background(), userID, payload))

	require.Equal(t, payload, <-userClient.Send)
	select {
	case <-otherClient.Send:
		t.Fatal("event was delivered to another user")
	default:
	}
}

func TestGatewayDropsSlowConnection(t *testing.T) {
	gateway := NewGateway()
	userID := uuid.New()
	client := gateway.RegisterWithBuffer(userID, 1)

	require.NoError(t, gateway.Deliver(context.Background(), userID, []byte("first")))
	require.NoError(t, gateway.Deliver(context.Background(), userID, []byte("second")))

	select {
	case <-gateway.UnregisterSignal():
	default:
		t.Fatal("slow connection was not scheduled for unregister")
	}
	_ = client
}

func TestGatewayContinuesDeliveryAfterSlowConnection(t *testing.T) {
	gateway := NewGateway()
	userID := uuid.New()
	healthy := gateway.RegisterWithBuffer(userID, 1)
	slow := gateway.RegisterWithBuffer(userID, 1)
	slow.Send <- []byte("occupied")

	require.NoError(t, gateway.Deliver(context.Background(), userID, []byte("payload")))
	require.Equal(t, []byte("payload"), <-healthy.Send)

	select {
	case dropped := <-gateway.UnregisterSignal():
		require.Same(t, slow, dropped)
	default:
		t.Fatal("slow connection was not scheduled for unregister")
	}
}

func TestGatewayNotifiesPresenceOnlyOnFirstAndLastConnection(t *testing.T) {
	probe := &gatewayPresenceProbe{
		connected:    make(chan uuid.UUID, 2),
		disconnected: make(chan uuid.UUID, 2),
	}
	gateway := NewGatewayWithPresence(probe)
	userID := uuid.New()

	first := gateway.Register(userID)
	second := gateway.Register(userID)

	select {
	case got := <-probe.connected:
		require.Equal(t, userID, got)
	case <-time.After(time.Second):
		t.Fatal("first connection did not trigger presence")
	}
	select {
	case <-probe.connected:
		t.Fatal("second connection triggered duplicate online presence")
	case <-time.After(50 * time.Millisecond):
	}

	gateway.Unregister(first)
	select {
	case <-probe.disconnected:
		t.Fatal("first disconnect triggered offline presence")
	case <-time.After(50 * time.Millisecond):
	}

	gateway.Unregister(second)
	select {
	case got := <-probe.disconnected:
		require.Equal(t, userID, got)
	case <-time.After(time.Second):
		t.Fatal("last disconnect did not trigger presence")
	}
}
