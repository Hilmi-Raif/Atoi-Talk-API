//go:build integration

package integration

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
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
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/captcha"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	"AtoiTalkAPI/internal/infrastructure/email"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	messageworker "AtoiTalkAPI/internal/messaging/message_worker"
	"AtoiTalkAPI/internal/websocket_gateway/auth"
	"AtoiTalkAPI/internal/websocket_gateway/connection"
	"AtoiTalkAPI/internal/websocket_gateway/presence"
	"AtoiTalkAPI/internal/websocket_gateway/pubsub"
	"AtoiTalkAPI/internal/websocket_gateway/stream"
	"AtoiTalkAPI/internal/websocket_gateway/typing"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

var (
	testClient              *ent.Client
	testConfig              *config.AppConfig
	testRouter              *chi.Mux
	redisAdapter            *redisinfra.RedisAdapter
	s3Client                *s3.Client
	testStorageAdapter      *objectstorage.StorageAdapter
	websocketGatewayHandler http.Handler
	integrationCancel       context.CancelFunc
	integrationStreamName   string
)

const (
	cfTurnstileAlwaysPasses      = "1x0000000000000000000000000000000AA"
	cfTurnstileAlwaysFails       = "2x0000000000000000000000000000000AA"
	cfTurnstileTokenAlreadySpent = "3x0000000000000000000000000000000AA"
	dummyTurnstileToken          = "DUMMY_TOKEN_XXXX"
)

func TestMain(m *testing.M) {
	_, b, _, _ := runtime.Caller(0)
	basepath := filepath.Dir(b)
	_ = godotenv.Load(filepath.Join(basepath, "../deployments/integration/.env.test"))
	_ = godotenv.Load(filepath.Join(basepath, "../deployments/integration/.env"))
	_ = godotenv.Load(filepath.Join(basepath, ".env.test"))
	_ = godotenv.Load(filepath.Join(basepath, "../.env.test"))
	_ = godotenv.Load(filepath.Join(basepath, "../.env"))

	if os.Getenv("APP_PORT") == "" {
		os.Setenv("APP_PORT", "8080")
	}
	if os.Getenv("APP_ENV") == "" {
		os.Setenv("APP_ENV", "test")
	}
	if os.Getenv("APP_URL") == "" {
		os.Setenv("APP_URL", "http://localhost:8080")
	}
	if os.Getenv("APP_CORS_ALLOWED_ORIGINS") == "" {
		os.Setenv("APP_CORS_ALLOWED_ORIGINS", "*")
	}
	os.Setenv("DB_MIGRATE", "true")

	if os.Getenv("JWT_SECRET") == "" {
		os.Setenv("JWT_SECRET", "secret")
	}
	if os.Getenv("JWT_EXP") == "" {
		os.Setenv("JWT_EXP", "86400")
	}
	if os.Getenv("OTP_SECRET") == "" {
		os.Setenv("OTP_SECRET", "secret")
	}
	if os.Getenv("OTP_EXP") == "" {
		os.Setenv("OTP_EXP", "300")
	}
	if os.Getenv("OTP_RATE_LIMIT_SECONDS") == "" {
		os.Setenv("OTP_RATE_LIMIT_SECONDS", "2")
	}
	if os.Getenv("TURNSTILE_SECRET_KEY") == "" {
		os.Setenv("TURNSTILE_SECRET_KEY", "1x0000000000000000000000000000000AA")
	}

	setEnvDefault("S3_BUCKET_PUBLIC", "test-public-bucket")
	setEnvDefault("S3_BUCKET_PRIVATE", "test-private-bucket")
	setEnvDefault("S3_REGION", "us-east-1")
	setEnvDefault("S3_ACCESS_KEY", "test")
	setEnvDefault("S3_SECRET_KEY", "test")
	setEnvDefault("S3_ENDPOINT", "http://localhost:9090")

	setEnvDefault("S3_PUBLIC_DOMAIN", os.Getenv("S3_ENDPOINT")+"/"+os.Getenv("S3_BUCKET_PUBLIC"))

	os.Setenv("SMTP_ASYNC", "false")

	testConfig = config.LoadAppConfig()

	testClient = config.InitEnt(testConfig)

	if err := testClient.Schema.Create(context.Background()); err != nil {
		log.Fatalf("failed creating schema resources: %v", err)
	}

	var redisErr error
	redisAdapter, redisErr = redisinfra.NewRedisAdapter(testConfig)
	if redisErr != nil {
		log.Fatalf("failed to connect Redis for tests: %v", redisErr)
	}
	eventPublisher := redisinfra.NewPubSubPublisher(testClient, redisAdapter.Client())

	repo := repository.NewRepository(testClient, redisAdapter, testConfig)

	validator := config.NewValidator()
	httpClient := config.NewHTTPClient()
	testRouter = config.NewChi(testConfig)

	emailAdapter := email.NewEmailAdapter(testConfig)

	s3Client = config.NewS3Client(testConfig)
	initS3Buckets(s3Client, testConfig.S3BucketPublic, testConfig.S3BucketPrivate)

	captchaAdapter := captcha.NewCaptchaAdapter(testConfig, httpClient)
	testStorageAdapter = objectstorage.NewStorageAdapter(testConfig, s3Client, httpClient)
	websocketGatewayHandler = startWebSocketGateway()

	otpService := authapp.NewOTPService(testClient, testConfig, validator, emailAdapter, captchaAdapter, redisAdapter, repo.RateLimit)
	otpController := controller.NewOTPController(otpService)

	authService := authapp.NewAuthService(testClient, testConfig, validator, testStorageAdapter, captchaAdapter, redisAdapter, otpService, repo.Session, eventPublisher)
	authController := controller.NewAuthController(authService)

	userService := userapp.NewUserService(testClient, repo.User, testConfig, validator, testStorageAdapter, eventPublisher, redisAdapter)
	userController := controller.NewUserController(userService)

	accountService := accountapp.NewAccountService(testClient, testConfig, validator, eventPublisher, otpService, repo.Session)
	accountController := controller.NewAccountController(accountService)

	chatService := chatapp.NewChatService(testClient, repo.Chat, repo.GroupMember, testConfig, validator, eventPublisher, testStorageAdapter, redisAdapter)
	privateChatService := chatapp.NewPrivateChatService(testClient, testConfig, validator, eventPublisher, redisAdapter, testStorageAdapter)
	groupChatService := groupapp.NewGroupChatService(testClient, repo.GroupMember, repo.GroupChat, testConfig, validator, eventPublisher, testStorageAdapter, redisAdapter)

	chatController := controller.NewChatController(chatService)
	privateChatController := controller.NewPrivateChatController(privateChatService)
	groupChatController := controller.NewGroupChatController(groupChatService)

	messageService := messageapp.NewMessageService(testClient, repo.Message, testConfig, validator, testStorageAdapter, eventPublisher)
	messageController := controller.NewMessageController(messageService)

	mediaService := mediaapp.NewMediaService(testClient, testConfig, validator, testStorageAdapter, captchaAdapter)
	mediaController := controller.NewMediaController(mediaService)

	reportService := adminapp.NewReportService(testClient, testConfig, validator, testStorageAdapter)
	reportController := controller.NewReportController(reportService)

	adminService := adminapp.NewAdminService(testClient, testConfig, validator, eventPublisher, repo.Session, repo.GroupMember, testStorageAdapter)
	adminController := controller.NewAdminController(adminService, groupChatService, validator)

	authMiddleware := middleware.NewAuthMiddleware(authService, repo.Session)
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(repo.RateLimit, testConfig)

	dbCheck := func(ctx context.Context) error {
		_, err := testClient.User.Query().Exist(ctx)
		return err
	}
	redisCheck := func(ctx context.Context) error {
		return redisAdapter.Client().Ping(ctx).Err()
	}

	route := routes.NewRoute(testConfig, testRouter, authController, otpController, userController, accountController, chatController, privateChatController, groupChatController, messageController, mediaController, reportController, adminController, authMiddleware, rateLimitMiddleware, dbCheck, redisCheck)
	route.Register()

	code := m.Run()

	if integrationCancel != nil {
		integrationCancel()
	}
	testClient.Close()
	os.Exit(code)
}

func setEnvDefault(key, value string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, value)
	}
}

func initS3Buckets(client *s3.Client, buckets ...string) {
	ctx := context.Background()
	for _, bucket := range buckets {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {

			log.Printf("Warning: Failed to create bucket %s: %v", bucket, err)
		}
	}
}

func executeRequest(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	return rr
}

func clearDatabase(ctx context.Context) {

	testClient.Report.Delete().Exec(ctx)
	testClient.MessageOutbox.Delete().Exec(ctx)
	testClient.Message.Delete().Exec(ctx)
	testClient.PrivateChat.Delete().Exec(ctx)
	testClient.GroupMember.Delete().Exec(ctx)
	testClient.GroupChat.Delete().Exec(ctx)
	testClient.Chat.Delete().Exec(ctx)
	testClient.Media.Delete().Exec(ctx)
	testClient.UserIdentity.Delete().Exec(ctx)
	testClient.User.Delete().Exec(ctx)

	if redisAdapter != nil {
		keys, err := redisAdapter.Client().Keys(ctx, "*").Result()
		if err == nil {
			for _, key := range keys {
				if key != integrationStreamName {
					redisAdapter.Client().Del(ctx, key)
				}
			}
		}
		if integrationStreamName != "" {
			redisAdapter.Client().XTrimMaxLen(ctx, integrationStreamName, 0)
		}
	}
}

func waitForPrivateUnreadCount(t *testing.T, chatID uuid.UUID, user1Count, user2Count int) {
	t.Helper()
	if !assert.Eventually(t, func() bool {
		privateEntity, err := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Only(context.Background())
		return err == nil && privateEntity.User1UnreadCount == user1Count && privateEntity.User2UnreadCount == user2Count
	}, 5*time.Second, 20*time.Millisecond) {
		privateEntity, err := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Only(context.Background())
		if err == nil {
			t.Logf("private unread counts: user1=%d user2=%d", privateEntity.User1UnreadCount, privateEntity.User2UnreadCount)
		}
	}
}

func waitForGroupUnreadCount(t *testing.T, groupChatID, userID uuid.UUID, expected int) {
	t.Helper()
	if !assert.Eventually(t, func() bool {
		member, err := testClient.GroupMember.Query().Where(
			groupmember.GroupChatID(groupChatID),
			groupmember.UserID(userID),
		).Only(context.Background())
		return err == nil && member.UnreadCount == expected
	}, 5*time.Second, 20*time.Millisecond) {
		member, err := testClient.GroupMember.Query().Where(
			groupmember.GroupChatID(groupChatID),
			groupmember.UserID(userID),
		).Only(context.Background())
		if err == nil {
			t.Logf("group unread count for user %s: %d", userID, member.UnreadCount)
		}
	}
}

func newWebSocketTestServer() *httptest.Server {
	return httptest.NewServer(websocketGatewayHandler)
}

func startWebSocketGateway() http.Handler {
	integrationCtx, cancel := context.WithCancel(context.Background())
	integrationCancel = cancel

	streamName := fmt.Sprintf("integration:message-events:%s", uuid.NewString())
	integrationStreamName = streamName
	group := fmt.Sprintf("integration-gateway:%s", uuid.NewString())
	consumerName := fmt.Sprintf("consumer:%s", uuid.NewString())

	presenceManager := presence.NewManager(
		redisAdapter.Client(),
		func(ctx context.Context, userID uuid.UUID) error {
			return testClient.User.UpdateOneID(userID).SetLastSeenAt(time.Now().UTC()).Exec(ctx)
		},
		integrationContacts,
		integrationBlocked,
	)
	gateway := connection.NewGatewayWithPresence(presenceManager)
	go gateway.Run(integrationCtx)

	pubSubListener := pubsub.NewPubSubListener(redisAdapter.Client(), "events:broadcast", gateway)
	go func() {
		if err := pubSubListener.Run(integrationCtx); err != nil && integrationCtx.Err() == nil {
			log.Printf("integration websocket Pub/Sub listener stopped: %v", err)
		}
	}()
	select {
	case <-pubSubListener.Ready():
	case <-time.After(5 * time.Second):
		log.Fatal("integration websocket Pub/Sub listener did not become ready")
	}

	streamConsumer := redisinfra.NewRedisStreamConsumer(redisAdapter.Client(), streamName, group, consumerName)
	if err := streamConsumer.EnsureGroup(integrationCtx); err != nil {
		log.Fatalf("failed to create integration websocket stream group: %v", err)
	}
	go stream.NewConsumer(streamConsumer, gateway).Run(integrationCtx)

	workerStore := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	workerPublisher := redisinfra.NewRedisStreamPublisher(redisAdapter.Client(), streamName, 10000)
	worker := messageworker.NewWorker(workerStore, workerPublisher, messageworker.WorkerConfig{
		BatchSize:     100,
		LeaseDuration: time.Minute,
		PollInterval:  20 * time.Millisecond,
	})
	go worker.Run(integrationCtx, nil)

	typingRouter := typing.NewRouterWithBlockChecker(
		redisAdapter.Client(),
		integrationChatMembers,
		func(ctx context.Context, senderID, targetID uuid.UUID) (bool, error) {
			return integrationBlocked(ctx, senderID, targetID)
		},
	)
	verifier := auth.NewJWTVerifier(testClient, redisAdapter.Client(), testConfig.JWTSecret)
	return connection.NewHTTPHandlerWithTypingRouter(gateway, verifier, testConfig.AppCorsAllowedOrigins, typingRouter)
}

func integrationContacts(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	privateChats, err := testClient.PrivateChat.Query().Where(
		privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID)),
	).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).All(ctx)
	if err != nil {
		return nil, err
	}
	contacts := make([]uuid.UUID, 0, len(privateChats))
	for _, privateChat := range privateChats {
		if privateChat.User1ID != nil && *privateChat.User1ID == userID && privateChat.User2ID != nil {
			contacts = append(contacts, *privateChat.User2ID)
		} else if privateChat.User2ID != nil && *privateChat.User2ID == userID && privateChat.User1ID != nil {
			contacts = append(contacts, *privateChat.User1ID)
		}
	}
	return contacts, nil
}

func integrationBlocked(ctx context.Context, firstID, secondID uuid.UUID) (bool, error) {
	return testClient.UserBlock.Query().Where(
		userblock.Or(userblock.BlockerID(firstID), userblock.BlockedID(firstID)),
		userblock.Or(userblock.BlockerID(secondID), userblock.BlockedID(secondID)),
	).Exist(ctx)
}

func integrationChatMembers(ctx context.Context, chatID uuid.UUID) ([]uuid.UUID, error) {
	chatEntity, err := testClient.Chat.Query().Where(chat.ID(chatID), chat.DeletedAtIsNil()).Select(chat.FieldType).Only(ctx)
	if err != nil {
		return nil, err
	}
	switch chatEntity.Type {
	case chat.TypePrivate:
		privateEntity, err := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).Only(ctx)
		if err != nil {
			return nil, err
		}
		members := make([]uuid.UUID, 0, 2)
		if privateEntity.User1ID != nil {
			members = append(members, *privateEntity.User1ID)
		}
		if privateEntity.User2ID != nil {
			members = append(members, *privateEntity.User2ID)
		}
		return members, nil
	case chat.TypeGroup:
		groupID, err := testClient.GroupChat.Query().Where(groupchat.ChatID(chatID)).OnlyID(ctx)
		if err != nil {
			return nil, err
		}
		members, err := testClient.GroupMember.Query().Where(groupmember.GroupChatID(groupID)).Select(groupmember.FieldUserID).All(ctx)
		if err != nil {
			return nil, err
		}
		userIDs := make([]uuid.UUID, 0, len(members))
		for _, member := range members {
			userIDs = append(userIDs, member.UserID)
		}
		return userIDs, nil
	default:
		return nil, nil
	}
}

func createOTP(email, code string, expiresAt time.Time) {
	duration := time.Until(expiresAt)
	if duration <= 0 {

		return
	}

	normalizedEmail := helper.NormalizeEmail(email)
	hashedCode := helper.HashOTP(code, testConfig.OTPSecret)

	key := fmt.Sprintf("otp:%s:%s", "register", normalizedEmail)
	if err := redisAdapter.Set(context.Background(), key, hashedCode, duration); err != nil {
		panic(fmt.Sprintf("failed to seed OTP for test: %v", err))
	}
}

func printBody(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Logf("Response Body: %s", rr.Body.String())
}

func createTestUser(t *testing.T, prefix string) *ent.User {
	email := fmt.Sprintf("%s%d@test.com", prefix, time.Now().UnixNano())
	username := fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	hashedPassword, _ := helper.HashPassword("Password123!")

	u, err := testClient.User.Create().
		SetEmail(email).
		SetUsername(username).
		SetFullName(prefix + " User").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleUser).
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create user %s: %v", prefix, err)
	}
	return u
}
