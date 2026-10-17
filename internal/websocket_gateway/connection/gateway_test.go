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
	deadlines    chan bool
}

func (p *gatewayPresenceProbe) Connected(ctx context.Context, userID uuid.UUID) {
	if p.deadlines != nil {
		_, ok := ctx.Deadline()
		p.deadlines <- ok
	}
	p.connected <- userID
}

func (p *gatewayPresenceProbe) Disconnected(ctx context.Context, userID uuid.UUID) {
	if p.deadlines != nil {
		_, ok := ctx.Deadline()
		p.deadlines <- ok
	}
	p.disconnected <- userID
}

func (p *gatewayPresenceProbe) Refresh(ctx context.Context, userID uuid.UUID) {
	if p.deadlines != nil {
		_, ok := ctx.Deadline()
		p.deadlines <- ok
	}
	p.refreshed <- userID
}

func TestGatewayBoundsPresenceOperations(t *testing.T) {
	probe := &gatewayPresenceProbe{
		connected:    make(chan uuid.UUID, 1),
		disconnected: make(chan uuid.UUID, 1),
		refreshed:    make(chan uuid.UUID, 1),
		deadlines:    make(chan bool, 3),
	}
	gateway := NewGatewayWithPresence(probe)
	client := gateway.Register(uuid.New())
	gateway.RefreshPresence(client)
	gateway.Unregister(client)

	require.True(t, <-probe.deadlines)
	require.True(t, <-probe.deadlines)
	require.True(t, <-probe.deadlines)
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

func TestGatewayUnregistersSlowConnectionWhenSignalQueueIsFull(t *testing.T) {
	gateway := NewGateway()
	for range cap(gateway.unregister) {
		gateway.unregister <- &Client{}
	}
	userID := uuid.New()
	client := gateway.RegisterWithBuffer(userID, 1)
	client.Send <- []byte("occupied")

	require.NoError(t, gateway.Deliver(context.Background(), userID, []byte("payload")))

	<-client.Send
	_, open := <-client.Send
	require.False(t, open)
	gateway.mu.RLock()
	_, registered := gateway.clients[userID]
	gateway.mu.RUnlock()
	require.False(t, registered)
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

func TestGatewayCloseAllClosesRegisteredClients(t *testing.T) {
	gateway := NewGateway()
	closed := make(chan struct{}, 2)
	first := gateway.RegisterWithClose(uuid.New(), 1, func() { closed <- struct{}{} })
	second := gateway.RegisterWithClose(uuid.New(), 1, func() { closed <- struct{}{} })

	gateway.CloseAll(context.Background())

	for range 2 {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("registered connection was not closed")
		}
	}
	_, firstOpen := <-first.Send
	_, secondOpen := <-second.Send
	require.False(t, firstOpen)
	require.False(t, secondOpen)
	require.Empty(t, gateway.clients)
}

func TestGatewayRejectsConnectionsRegisteredDuringShutdown(t *testing.T) {
	gateway := NewGateway()
	gateway.CloseAll(context.Background())
	closed := make(chan struct{})

	client := gateway.RegisterWithClose(uuid.New(), 1, func() { close(closed) })

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection registered during shutdown was not closed")
	}
	_, open := <-client.Send
	require.False(t, open)
	require.Empty(t, gateway.clients)
}

func TestGatewayCloseAllUsesOneSharedDeadline(t *testing.T) {
	presence := &blockingDisconnectPresence{}
	gateway := NewGatewayWithPresence(presence)
	gateway.Register(uuid.New())
	gateway.Register(uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	gateway.CloseAll(ctx)

	require.Less(t, time.Since(started), 200*time.Millisecond)
}

type blockingDisconnectPresence struct{}

func (*blockingDisconnectPresence) Connected(context.Context, uuid.UUID) {}
func (*blockingDisconnectPresence) Refresh(context.Context, uuid.UUID)   {}
func (*blockingDisconnectPresence) Disconnected(ctx context.Context, _ uuid.UUID) {
	<-ctx.Done()
}

func TestGatewayRegisterInstallsCloseCallbackBeforeClientBecomesVisible(t *testing.T) {
	gateway := NewGateway()
	closed := make(chan struct{}, 1)
	client := gateway.RegisterWithClose(uuid.New(), 1, func() { close(closed) })

	gateway.Unregister(client)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection close callback was not called")
	}
}

func TestGatewayDoesNotPublishOfflineBeforeOnlineTransitionCompletes(t *testing.T) {
	presence := &orderedPresenceProbe{
		connectedStarted: make(chan struct{}),
		allowConnected:   make(chan struct{}),
		disconnected:     make(chan struct{}, 1),
	}
	gateway := NewGatewayWithPresence(presence)
	userID := uuid.New()
	registered := make(chan *Client, 1)
	go func() { registered <- gateway.Register(userID) }()

	<-presence.connectedStarted
	gateway.mu.RLock()
	var client *Client
	for candidate := range gateway.clients[userID] {
		client = candidate
	}
	gateway.mu.RUnlock()
	require.NotNil(t, client)

	unregistered := make(chan struct{})
	go func() {
		gateway.Unregister(client)
		close(unregistered)
	}()
	select {
	case <-presence.disconnected:
		t.Fatal("offline transition ran before online transition completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(presence.allowConnected)
	<-registered
	select {
	case <-presence.disconnected:
	case <-time.After(time.Second):
		t.Fatal("offline transition did not run after online transition completed")
	}
	<-unregistered
}

type orderedPresenceProbe struct {
	connectedStarted chan struct{}
	allowConnected   chan struct{}
	disconnected     chan struct{}
}

func (p *orderedPresenceProbe) Connected(context.Context, uuid.UUID) {
	close(p.connectedStarted)
	<-p.allowConnected
}

func (*orderedPresenceProbe) Refresh(context.Context, uuid.UUID) {}

func (p *orderedPresenceProbe) Disconnected(context.Context, uuid.UUID) {
	p.disconnected <- struct{}{}
}
