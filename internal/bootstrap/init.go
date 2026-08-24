package bootstrap

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/controller"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/service"
	"AtoiTalkAPI/internal/websocket"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
)

func Init(appConfig *config.AppConfig, client *ent.Client, validator *validator.Validate, s3Client *s3.Client, httpClient *http.Client, chiMux *chi.Mux) {

	storageAdapter := adapter.NewStorageAdapter(appConfig, s3Client, httpClient)
	emailAdapter := adapter.NewEmailAdapter(appConfig)
	captchaAdapter := adapter.NewCaptchaAdapter(appConfig, httpClient)
	redisAdapter, err := adapter.NewRedisAdapter(appConfig)
	if err != nil {
		slog.Error("Failed to initialize Redis adapter", "error", err)
		os.Exit(1)
	}

	wsHub := websocket.NewHub(client, redisAdapter)
	go wsHub.Run()

	repo := repository.NewRepository(client, redisAdapter, appConfig)

	otpService := service.NewOTPService(client, appConfig, validator, emailAdapter, captchaAdapter, redisAdapter, repo.RateLimit)

	authService := service.NewAuthService(client, appConfig, validator, storageAdapter, captchaAdapter, redisAdapter, otpService, repo.Session, wsHub)

	accountService := service.NewAccountService(client, appConfig, validator, wsHub, otpService, repo.Session)

	userService := service.NewUserService(client, repo.User, appConfig, validator, storageAdapter, wsHub, redisAdapter)
	chatService := service.NewChatService(client, repo.Chat, repo.GroupMember, appConfig, validator, wsHub, storageAdapter, redisAdapter)
	privateChatService := service.NewPrivateChatService(client, appConfig, validator, wsHub, redisAdapter, storageAdapter)
	groupChatService := service.NewGroupChatService(client, repo.GroupMember, repo.GroupChat, appConfig, validator, wsHub, storageAdapter, redisAdapter)
	messageService := service.NewMessageService(client, repo.Message, appConfig, validator, storageAdapter, wsHub)
	mediaService := service.NewMediaService(client, appConfig, validator, storageAdapter, captchaAdapter)
	reportService := service.NewReportService(client, appConfig, validator, storageAdapter)

	adminService := service.NewAdminService(client, appConfig, validator, wsHub, repo.Session, repo.GroupMember, storageAdapter)

	authController := controller.NewAuthController(authService)
	otpController := controller.NewOTPController(otpService)
	userController := controller.NewUserController(userService)
	accountController := controller.NewAccountController(accountService)
	chatController := controller.NewChatController(chatService)
	privateChatController := controller.NewPrivateChatController(privateChatService)
	groupChatController := controller.NewGroupChatController(groupChatService)
	messageController := controller.NewMessageController(messageService)
	mediaController := controller.NewMediaController(mediaService)
	reportController := controller.NewReportController(reportService)
	adminController := controller.NewAdminController(adminService, groupChatService, validator)
	wsController := controller.NewWebSocketController(wsHub, appConfig)

	authMiddleware := middleware.NewAuthMiddleware(authService, repo.Session)
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(repo.RateLimit, appConfig)

	route := NewRoute(appConfig, chiMux, authController, otpController, userController, accountController, chatController, privateChatController, groupChatController, messageController, mediaController, wsController, reportController, adminController, authMiddleware, rateLimitMiddleware)
	route.Register()
}
