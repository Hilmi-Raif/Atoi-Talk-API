package routes

import (
	"AtoiTalkAPI/internal/api/http/controller"
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestRouteRegisterExposesOperationalAndAPIRoutes(t *testing.T) {
	router := chi.NewRouter()
	route := NewRoute(
		&config.AppConfig{OTelServiceName: "test-api"},
		router,
		&controller.AuthController{},
		&controller.OTPController{},
		&controller.UserController{},
		&controller.AccountController{},
		&controller.ChatController{},
		&controller.PrivateChatController{},
		&controller.GroupChatController{},
		&controller.MessageController{},
		&controller.MediaController{},
		&controller.ReportController{},
		&controller.AdminController{},
		&middleware.AuthMiddleware{},
		&middleware.RateLimitMiddleware{},
		func(_ context.Context) error { return nil },
		func(_ context.Context) error { return nil },
	)

	route.Register()

	routes := make(map[string]map[string]bool)
	require.NoError(t, chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if routes[path] == nil {
			routes[path] = make(map[string]bool)
		}
		routes[path][method] = true
		return nil
	}))

	for _, expected := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/readyz"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/chats"},
		{http.MethodGet, "/api/chats/{chatID}/messages"},
		{http.MethodPost, "/api/messages"},
		{http.MethodGet, "/api/admin/dashboard"},
	} {
		require.True(t, routes[expected.path][expected.method], "%s %s is not registered", expected.method, expected.path)
	}
}
