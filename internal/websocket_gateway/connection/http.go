package connection

import (
	protocol "AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var ErrUnauthorized = errors.New("unauthorized websocket token")

type TokenVerifier interface {
	Verify(context.Context, string) (uuid.UUID, error)
}

type TypingPublisher interface {
	Publish(context.Context, uuid.UUID, protocol.Event) error
}

type HTTPHandler struct {
	gateway        *Gateway
	verifier       TokenVerifier
	typingRouter   TypingPublisher
	allowAllOrigin bool
	allowedOrigins map[string]struct{}
}

func NewHTTPHandler(gateway *Gateway, verifier TokenVerifier, origins []string) *HTTPHandler {
	return NewHTTPHandlerWithTypingRouter(gateway, verifier, origins, nil)
}

func NewHTTPHandlerWithTypingRouter(gateway *Gateway, verifier TokenVerifier, origins []string, typingRouter TypingPublisher) *HTTPHandler {
	handler := &HTTPHandler{
		gateway:        gateway,
		verifier:       verifier,
		typingRouter:   typingRouter,
		allowedOrigins: make(map[string]struct{}),
	}
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			handler.allowAllOrigin = true
			continue
		}
		if origin != "" {
			handler.allowedOrigins[strings.ToLower(origin)] = struct{}{}
		}
	}
	return handler
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}

	token := ExtractWebSocketToken(r)
	if token == "" || h.verifier == nil {
		http.Error(w, ErrUnauthorized.Error(), http.StatusUnauthorized)
		return
	}
	userID, err := h.verifier.Verify(r.Context(), token)
	if err != nil {
		http.Error(w, ErrUnauthorized.Error(), http.StatusUnauthorized)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     h.checkOrigin,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := h.gateway.Register(userID)
	client.close = func() { _ = conn.Close() }
	go h.writePump(conn, client)
	go h.readPump(conn, client)
}

func (h *HTTPHandler) checkOrigin(r *http.Request) bool {
	if h.allowAllOrigin || strings.TrimSpace(r.Header.Get("Origin")) == "" {
		return true
	}
	_, ok := h.allowedOrigins[strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))]
	return ok
}

func (h *HTTPHandler) writePump(conn *websocket.Conn, client *Client) {
	ticker := time.NewTicker((60 * time.Second * 9) / 10)
	defer func() {
		ticker.Stop()
		h.gateway.Unregister(client)
		_ = conn.Close()
	}()
	for {
		select {
		case payload, ok := <-client.Send:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *HTTPHandler) readPump(conn *websocket.Conn, client *Client) {
	defer func() {
		h.gateway.Unregister(client)
		_ = conn.Close()
	}()
	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = h.handleIncomingMessage(context.Background(), client.UserID, message)
	}
}

func (h *HTTPHandler) handleIncomingMessage(ctx context.Context, userID uuid.UUID, data []byte) error {
	if h.typingRouter == nil {
		return nil
	}
	var event protocol.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	return h.typingRouter.Publish(ctx, userID, event)
}
