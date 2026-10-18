package controller

import (
	"AtoiTalkAPI/ent/enttest"
	pushapp "AtoiTalkAPI/internal/api/application/push"
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestNotificationControllerPreferences(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, dialect.SQLite, "file:notification-controller?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	owner, peer := db.User.Create().SaveX(ctx), db.User.Create().SaveX(ctx)
	chat := db.Chat.Create().SetType("private").SaveX(ctx)
	db.PrivateChat.Create().SetChatID(chat.ID).SetUser1ID(owner.ID).SetUser2ID(peer.ID).SaveX(ctx)
	c := NewNotificationController(pushapp.NewService(db, &config.AppConfig{}))
	call := func(handler http.HandlerFunc, method, path, body, id string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("chatID", id)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
		if authenticated {
			r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: owner.ID}))
		}
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	for _, handler := range []http.HandlerFunc{c.GetSettings, c.UpdateSettings, c.Chat, c.MutedChats} {
		require.Equal(t, 401, call(handler, "GET", "/", "", "", false).Code)
	}
	require.Equal(t, 200, call(c.GetSettings, "GET", "/", "", "", true).Code)
	for _, body := range []string{"{", `{}`, `{"unknown":true}`, `{"message_notifications_enabled":"yes"}`} {
		require.Equal(t, 400, call(c.UpdateSettings, "PUT", "/", body, "", true).Code)
	}
	require.Equal(t, 200, call(c.UpdateSettings, "PUT", "/", `{"message_notifications_enabled":false}`, "", true).Code)
	require.False(t, db.User.GetX(ctx, owner.ID).MessageNotificationsEnabled)
	require.True(t, db.User.GetX(ctx, peer.ID).MessageNotificationsEnabled)
	for _, id := range []string{"invalid", uuid.Nil.String()} {
		require.Equal(t, 400, call(c.Chat, "PUT", "/", `{}`, id, true).Code)
	}
	require.Equal(t, 400, call(c.Chat, "PUT", "/", `{"unknown":true}`, chat.ID.String(), true).Code)
	require.Equal(t, 404, call(c.Chat, "PUT", "/", `{}`, uuid.NewString(), true).Code)
	require.Equal(t, 200, call(c.Chat, "PUT", "/", `{"muted_forever":true}`, chat.ID.String(), true).Code)
	require.Equal(t, 1, db.ChatNotificationPreference.Query().CountX(ctx))
	require.Equal(t, 200, call(c.MutedChats, "GET", "/?limit=1", "", "", true).Code)
	for _, query := range []string{"?limit=no", "?limit=0", "?cursor=invalid"} {
		require.Equal(t, 400, call(c.MutedChats, "GET", "/"+query, "", "", true).Code)
	}
	require.Equal(t, 200, call(c.Chat, "PUT", "/", `{}`, chat.ID.String(), true).Code)
	require.Zero(t, db.ChatNotificationPreference.Query().CountX(ctx))
	require.NoError(t, db.Close())
	require.Equal(t, 500, call(c.GetSettings, "GET", "/", "", "", true).Code)
	require.Equal(t, 500, call(c.UpdateSettings, "PUT", "/", `{"message_notifications_enabled":true}`, "", true).Code)
	require.Equal(t, 500, call(c.MutedChats, "GET", "/", "", "", true).Code)
}
