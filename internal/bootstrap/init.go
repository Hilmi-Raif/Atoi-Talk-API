package bootstrap

import (
	"AtoiTalkAPI/ent"
	accountapp "AtoiTalkAPI/internal/api/application/account"
	adminapp "AtoiTalkAPI/internal/api/application/admin"
	authapp "AtoiTalkAPI/internal/api/application/auth"
	chatapp "AtoiTalkAPI/internal/api/application/chat"
	groupapp "AtoiTalkAPI/internal/api/application/group"
	mediaapp "AtoiTalkAPI/internal/api/application/media"
	messageapp "AtoiTalkAPI/internal/api/application/message"
	userapp "AtoiTalkAPI/internal/api/application/user"
	"AtoiTalkAPI/internal/api/http/controller"
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/api/http/routes"
	"AtoiTalkAPI/internal/infrastructure/captcha"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	"AtoiTalkAPI/internal/infrastructure/email"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/redis"
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
)

func Init(appConfig *config.AppConfig, client *ent.Client, validator *validator.Validate, s3Client *s3.Client, httpClient *http.Client, chiMux *chi.Mux) {

	storageAdapter := objectstorage.NewStorageAdapter(appConfig, s3Client, httpClient)
	emailAdapter := email.NewEmailAdapter(appConfig)
	captchaAdapter := captcha.NewCaptchaAdapter(appConfig, httpClient)
	redisAdapter, err := redis.NewRedisAdapter(appConfig)
	if err != nil {
		slog.Error("Failed to initialize Redis adapter", "error", err)
		os.Exit(1)
	}

	eventPublisher := redis.NewPubSubPublisher(client, redisAdapter.Client())

	repo := repository.NewRepository(client, redisAdapter, appConfig)

	otpService := authapp.NewOTPService(client, appConfig, validator, emailAdapter, captchaAdapter, redisAdapter, repo.RateLimit)

	authService := authapp.NewAuthService(client, appConfig, validator, storageAdapter, captchaAdapter, redisAdapter, otpService, repo.Session, eventPublisher)

	accountService := accountapp.NewAccountService(client, appConfig, validator, eventPublisher, otpService, repo.Session)

	userService := userapp.NewUserService(client, repo.User, appConfig, validator, storageAdapter, eventPublisher, redisAdapter)
	chatService := chatapp.NewChatService(client, repo.Chat, repo.GroupMember, appConfig, validator, eventPublisher, storageAdapter, redisAdapter)
	privateChatService := chatapp.NewPrivateChatService(client, appConfig, validator, eventPublisher, redisAdapter, storageAdapter)
	groupChatService := groupapp.NewGroupChatService(client, repo.GroupMember, repo.GroupChat, appConfig, validator, eventPublisher, storageAdapter, redisAdapter)
	messageService := messageapp.NewMessageService(client, repo.Message, appConfig, validator, storageAdapter, eventPublisher)
	mediaService := mediaapp.NewMediaService(client, appConfig, validator, storageAdapter, captchaAdapter)
	reportService := adminapp.NewReportService(client, appConfig, validator, storageAdapter)

	adminService := adminapp.NewAdminService(client, appConfig, validator, eventPublisher, repo.Session, repo.GroupMember, storageAdapter)

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
	authMiddleware := middleware.NewAuthMiddleware(authService, repo.Session)
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(repo.RateLimit, appConfig)

	dbCheck := func(ctx context.Context) error {
		_, err := client.User.Query().Exist(ctx)
		return err
	}
	redisCheck := func(ctx context.Context) error {
		return redisAdapter.Client().Ping(ctx).Err()
	}

	route := routes.NewRoute(appConfig, chiMux, authController, otpController, userController, accountController, chatController, privateChatController, groupChatController, messageController, mediaController, reportController, adminController, authMiddleware, rateLimitMiddleware, dbCheck, redisCheck)
	route.Register()
}
