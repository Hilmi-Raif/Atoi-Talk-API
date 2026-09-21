package connection

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

const defaultClientBuffer = 256

type Client struct {
	UserID uuid.UUID
	Send   chan []byte
	close  func()
}

type PresenceListener interface {
	Connected(context.Context, uuid.UUID)
	Disconnected(context.Context, uuid.UUID)
}

type Gateway struct {
	mu         sync.RWMutex
	clients    map[uuid.UUID]map[*Client]struct{}
	unregister chan *Client
	presence   PresenceListener
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
	return g.RegisterWithBuffer(userID, defaultClientBuffer)
}

func (g *Gateway) RegisterWithBuffer(userID uuid.UUID, buffer int) *Client {
	if buffer <= 0 {
		buffer = defaultClientBuffer
	}
	client := &Client{UserID: userID, Send: make(chan []byte, buffer)}
	g.mu.Lock()
	firstConnection := g.clients[userID] == nil
	if firstConnection {
		g.clients[userID] = make(map[*Client]struct{})
	}
	g.clients[userID][client] = struct{}{}
	g.mu.Unlock()
	if firstConnection && g.presence != nil {
		g.presence.Connected(context.Background(), userID)
	}
	return client
}

func (g *Gateway) Deliver(ctx context.Context, userID uuid.UUID, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	for client := range g.clients[userID] {
		select {
		case client.Send <- append([]byte(nil), payload...):
		default:
			select {
			case g.unregister <- client:
			default:
			}
			return errors.New("realtime client send buffer is full")
		}
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

func (g *Gateway) Unregister(client *Client) {
	if client == nil {
		return
	}
	lastConnection := false
	g.mu.Lock()
	if clients := g.clients[client.UserID]; clients != nil {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			close(client.Send)
			if client.close != nil {
				client.close()
			}
			lastConnection = len(clients) == 0
			if lastConnection {
				delete(g.clients, client.UserID)
			}
		}
	}
	g.mu.Unlock()
	if lastConnection && g.presence != nil {
		g.presence.Disconnected(context.Background(), client.UserID)
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
