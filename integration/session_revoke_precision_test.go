//go:build integration

package integration

import (
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRevoke_AllowsNewTokenWithIatMillisInSameSecond(t *testing.T) {
	clearDatabase(context.Background())

	u := createTestUser(t, "revokeprecision")

	issuedAtSecond := time.Now().UTC().Truncate(time.Second)
	revokedAt := issuedAtSecond.UnixMilli() + 500
	tokenIssuedAtMillis := revokedAt + 1

	revokeKey := fmt.Sprintf("revoked_user:%s", u.ID)
	err := redisAdapter.Set(
		context.Background(),
		revokeKey,
		strconv.FormatInt(revokedAt, 10),
		time.Duration(testConfig.JWTExp)*time.Second,
	)
	if !assert.NoError(t, err) {
		return
	}

	claims := helper.JWTClaims{
		UserID:         u.ID,
		IssuedAtMillis: tokenIssuedAtMillis,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(issuedAtSecond),
			ExpiresAt: jwt.NewNumericDate(issuedAtSecond.Add(5 * time.Minute)),
			Issuer:    "AtoiTalkAPI",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(testConfig.JWTSecret))
	if !assert.NoError(t, err) {
		return
	}

	rr := makeRequest(http.MethodGet, "/api/user/current", nil, tokenString)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRevokedOpenWebSocketCannotPublishTyping(t *testing.T) {
	clearDatabase(context.Background())
	sender := createWSUser(t, "revokedsender", "revokedsender@example.com")
	recipient := createWSUser(t, "revokedrecipient", "revokedrecipient@example.com")
	senderToken, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, sender.ID)
	require.NoError(t, err)
	recipientToken, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, recipient.ID)
	require.NoError(t, err)
	createWSPrivateChat(t, recipient.ID, senderToken)
	chats, err := testClient.Chat.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, chats, 1)

	server := newWebSocketTestServer()
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token="
	senderConn, _, err := ws.DefaultDialer.Dial(wsURL+senderToken, nil)
	require.NoError(t, err)
	defer senderConn.Close()
	recipientConn, _, err := ws.DefaultDialer.Dial(wsURL+recipientToken, nil)
	require.NoError(t, err)
	defer recipientConn.Close()

	// The socket was accepted before the user-wide revocation marker was written.
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, redisAdapter.Set(context.Background(), "revoked_user:"+sender.ID.String(), strconv.FormatInt(time.Now().UTC().UnixMilli(), 10), time.Duration(testConfig.JWTExp)*time.Second))
	require.NoError(t, senderConn.WriteJSON(events.Event{Type: events.EventTyping, Meta: &events.EventMeta{ChatID: chats[0].ID}}))
	var closeErr *ws.CloseError
	require.NoError(t, senderConn.SetReadDeadline(time.Now().Add(time.Second)))
	for {
		_, _, err = senderConn.ReadMessage()
		if err != nil {
			break
		}
	}
	require.ErrorAs(t, err, &closeErr, "revoked socket should close instead of remaining active")
	require.Nil(t, waitForEvent(t, recipientConn, events.EventTyping, 200*time.Millisecond), "recipient must not receive typing from a revoked socket")
}

func TestRevokedSessionCannotReconnectWebSocket(t *testing.T) {
	clearDatabase(context.Background())
	u := createTestUser(t, "wsrevoked")
	issuedAtSecond := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	revokedAt := issuedAtSecond.UnixMilli() + 500
	newToken := func(issuedAtMillis int64) string {
		t.Helper()
		claims := helper.JWTClaims{
			UserID:         u.ID,
			IssuedAtMillis: issuedAtMillis,
			RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(issuedAtSecond),
				ExpiresAt: jwt.NewNumericDate(issuedAtSecond.Add(5 * time.Minute)),
				Issuer:    "AtoiTalkAPI",
			},
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testConfig.JWTSecret))
		if !assert.NoError(t, err) {
			return ""
		}
		return token
	}
	if !assert.NoError(t, redisAdapter.Set(context.Background(), fmt.Sprintf("revoked_user:%s", u.ID), strconv.FormatInt(revokedAt, 10), time.Duration(testConfig.JWTExp)*time.Second)) {
		return
	}
	server := newWebSocketTestServer()
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token="
	oldToken := newToken(revokedAt - 1)
	assert.Equal(t, http.StatusUnauthorized, makeRequest(http.MethodGet, "/api/user/current", nil, oldToken).Code)
	conn, response, err := ws.DefaultDialer.Dial(wsURL+oldToken, nil)
	assert.Error(t, err)
	if conn != nil {
		_ = conn.Close()
	}
	if assert.NotNil(t, response) {
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	}
	validToken := newToken(revokedAt + 1)
	assert.Equal(t, http.StatusOK, makeRequest(http.MethodGet, "/api/user/current", nil, validToken).Code)
	conn, _, err = ws.DefaultDialer.Dial(wsURL+validToken, nil)
	if assert.NoError(t, err) {
		_ = conn.Close()
	}
}
