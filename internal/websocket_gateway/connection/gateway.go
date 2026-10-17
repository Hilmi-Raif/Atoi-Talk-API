package connection

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultClientBuffer = 256
	dependencyTimeout   = 5 * time.Second
)

type Client struct {
	UserID uuid.UUID
	Send   chan []byte
	close  func()
	ready  chan struct{}
}

type PresenceListener interface {
	Connected(context.Context, uuid.UUID)
	Refresh(context.Context, uuid.UUID)
	Disconnected(context.Context, uuid.UUID)
}

type Gateway struct {
	mu         sync.RWMutex
	clients    map[uuid.UUID]map[*Client]struct{}
	unregister chan *Client
	presence   PresenceListener
	closing    bool
}

func NewGateway() *Gateway {
	return NewGatewayWithPresence(nil)
}

func NewGatewayWithPresence(presence PresenceListener) *Gateway {
	return &Gateway{
		clients:    make(map[uuid.UUID]map[*Client]struct{}),
		unregister: make(chan *Client, defaultClientBuffer),
		presence:   presence,
	}
}

func (g *Gateway) Register(userID uuid.UUID) *Client {
	return g.RegisterWithClose(userID, defaultClientBuffer, nil)
}

func (g *Gateway) RegisterWithBuffer(userID uuid.UUID, buffer int) *Client {
	return g.RegisterWithClose(userID, buffer, nil)
}

func (g *Gateway) RegisterWithClose(userID uuid.UUID, buffer int, closeFn func()) *Client {
	if buffer <= 0 {
		buffer = defaultClientBuffer
	}
	client := &Client{UserID: userID, Send: make(chan []byte, buffer), close: closeFn, ready: make(chan struct{})}
	g.mu.Lock()
	if g.closing {
		close(client.Send)
		close(client.ready)
		g.mu.Unlock()
		if closeFn != nil {
			closeFn()
		}
		return client
	}
	firstConnection := g.clients[userID] == nil
	if firstConnection {
		g.clients[userID] = make(map[*Client]struct{})
	}
	g.clients[userID][client] = struct{}{}
	g.mu.Unlock()
	if firstConnection && g.presence != nil {
		ctx, cancel := context.WithTimeout(context.Background(), dependencyTimeout)
		g.presence.Connected(ctx, userID)
		cancel()
	}
	close(client.ready)
	return client
}

func (g *Gateway) Deliver(ctx context.Context, userID uuid.UUID, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	g.mu.RLock()
	dropped := make([]*Client, 0)
	for client := range g.clients[userID] {
		select {
		case client.Send <- append([]byte(nil), payload...):
		default:
			slog.WarnContext(ctx, "dropping realtime delivery for slow client", "userID", userID)
			select {
			case g.unregister <- client:
			default:
				dropped = append(dropped, client)
			}
		}
	}
	g.mu.RUnlock()
	for _, client := range dropped {
		g.unregisterClient(ctx, client)
	}
	return nil
}

func (g *Gateway) DeliverString(ctx context.Context, userID string, payload []byte) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	return g.Deliver(ctx, id, payload)
}

func (g *Gateway) RefreshPresence(client *Client) {
	if client == nil || g.presence == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dependencyTimeout)
	defer cancel()
	g.presence.Refresh(ctx, client.UserID)
}

func (g *Gateway) Unregister(client *Client) {
	g.unregisterClient(context.Background(), client)
}

func (g *Gateway) unregisterClient(ctx context.Context, client *Client) {
	if client == nil {
		return
	}
	lastConnection := false
	var closeFn func()
	g.mu.Lock()
	if clients := g.clients[client.UserID]; clients != nil {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			close(client.Send)
			closeFn = client.close
			lastConnection = len(clients) == 0
			if lastConnection {
				delete(g.clients, client.UserID)
			}
		}
	}
	g.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	if lastConnection && g.presence != nil {
		if client.ready != nil {
			select {
			case <-client.ready:
			case <-ctx.Done():
				return
			}
		}
		ctx, cancel := context.WithTimeout(ctx, dependencyTimeout)
		defer cancel()
		g.presence.Disconnected(ctx, client.UserID)
	}
}

func (g *Gateway) CloseAll(ctx context.Context) {
	g.mu.Lock()
	g.closing = true
	clients := make([]*Client, 0)
	for _, userClients := range g.clients {
		for client := range userClients {
			clients = append(clients, client)
		}
	}
	g.mu.Unlock()
	for _, client := range clients {
		g.unregisterClient(ctx, client)
	}
}

func (g *Gateway) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-g.unregister:
			g.Unregister(client)
		}
	}
}

func (g *Gateway) UnregisterSignal() <-chan *Client {
	return g.unregister
}
