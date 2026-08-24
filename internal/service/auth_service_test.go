package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/user"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/constant"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func newMockAuthService(t *testing.T, client *ent.Client) (*AuthService, *adaptermocks.MockCaptchaVerifier, *adaptermocks.MockRedisOAuthStore, *repositorymocks.MockSessionStore, *adaptermocks.MockAuthStorage) {
	t.Helper()
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	redisStore := adaptermocks.NewMockRedisOAuthStore(t)
	session := repositorymocks.NewMockSessionStore(t)
	storage := adaptermocks.NewMockAuthStorage(t)
	service := NewAuthService(
		client,
		&config.AppConfig{JWTSecret: "test-secret", JWTExp: 3600, OTPSecret: "otp-secret", GoogleClientID: "client-id", GoogleClientSecret: "client-secret", GoogleRedirectURL: "http://localhost/callback"},
		config.NewValidator(),
		storage,
		captcha,
		redisStore,
		nil,
		session,
		nil,
	)
	return service, captcha, redisStore, session, storage
}

func newAuthTestClient(t *testing.T) *ent.Client {
	t.Helper()
	return enttest.Open(t, dialect.SQLite, "file:auth-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
}

func TestAuthServiceBeginGoogleAuthStoresPKCEState(t *testing.T) {
	service, _, redisStore, _, _ := newMockAuthService(t, nil)
	redisStore.EXPECT().Set(mock.Anything, mock.MatchedBy(func(key string) bool {
		return len(key) > len(googleOAuthStateKeyPrefix) && key[:len(googleOAuthStateKeyPrefix)] == googleOAuthStateKeyPrefix
	}), mock.Anything, googleOAuthStateTTL).Return(nil)

	result, err := service.BeginGoogleAuth(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result.State)
	require.Contains(t, result.AuthURL, "code_challenge=")
	require.Equal(t, int(googleOAuthStateTTL.Seconds()), result.ExpiresInSeconds)
}

func TestAuthServiceBeginGoogleAuthReturnsServiceUnavailableWhenRedisFails(t *testing.T) {
	service, _, redisStore, _, _ := newMockAuthService(t, nil)
	redisStore.EXPECT().Set(mock.Anything, mock.Anything, mock.Anything, googleOAuthStateTTL).Return(errors.New("redis unavailable"))

	result, err := service.BeginGoogleAuth(context.Background())

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceBeginGoogleAuthBindsClientFingerprint(t *testing.T) {
	service, _, redisStore, _, _ := newMockAuthService(t, nil)
	redisStore.EXPECT().Set(mock.Anything, mock.Anything, mock.MatchedBy(func(value interface{}) bool {
		payload, ok := value.([]byte)
		return ok && strings.Contains(string(payload), "fingerprint_hash")
	}), googleOAuthStateTTL).Return(nil)

	_, err := service.BeginGoogleAuth(helper.WithClientFingerprint(context.Background(), "browser-fingerprint"))

	require.NoError(t, err)
}

func TestAuthServiceGoogleExchangeRejectsInvalidRequestBeforeRedis(t *testing.T) {
	service, _, redisStore, _, _ := newMockAuthService(t, nil)

	result, err := service.GoogleExchange(context.Background(), model.GoogleLoginRequest{Code: ""})

	require.Nil(t, result)
	require.Error(t, err)
	redisStore.AssertNotCalled(t, "GetDel", mock.Anything, mock.Anything)
}

func TestAuthServiceGoogleExchangeRejectsMissingOrMalformedState(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		err     error
	}{
		{name: "missing", err: redis.Nil},
		{name: "malformed", payload: "not-json"},
		{name: "missing verifier", payload: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, redisStore, _, _ := newMockAuthService(t, nil)
			redisStore.EXPECT().GetDel(mock.Anything, googleOAuthStateKeyPrefix+"valid-state-123456").Return(tt.payload, tt.err)

			result, err := service.GoogleExchange(context.Background(), model.GoogleLoginRequest{
				Code:  "authorization-code",
				State: "valid-state-123456",
			})

			require.Nil(t, result)
			require.Error(t, err)
		})
	}
}

func TestAuthServiceGoogleExchangeRejectsFingerprintMismatch(t *testing.T) {
	service, _, redisStore, _, _ := newMockAuthService(t, nil)
	payload := `{"code_verifier":"verifier","fingerprint_hash":"different"}`
	redisStore.EXPECT().GetDel(mock.Anything, googleOAuthStateKeyPrefix+"valid-state-123456").Return(payload, nil)

	ctx := helper.WithClientFingerprint(context.Background(), "fingerprint")
	result, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "authorization-code",
		State: "valid-state-123456",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceLogoutBlacklistsInvalidTokenWithDefaultTTL(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	session.EXPECT().BlacklistToken(mock.Anything, "invalid-token", time.Hour).Return(nil)

	err := service.Logout(context.Background(), "invalid-token")

	require.NoError(t, err)
}

func TestAuthServiceLogoutUsesTokenExpiryTTL(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	userID := uuid.New()
	token, err := helper.GenerateJWT("test-secret", 3600, userID)
	require.NoError(t, err)
	session.EXPECT().BlacklistToken(mock.Anything, token, mock.MatchedBy(func(ttl time.Duration) bool {
		return ttl > 0 && ttl <= time.Hour
	})).Return(nil)

	require.NoError(t, service.Logout(context.Background(), token))
}

func TestAuthServiceLogoutDisconnectsUserWhenHubPresent(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	userID := uuid.New()
	token, err := helper.GenerateJWT("test-secret", 3600, userID)
	require.NoError(t, err)

	session.EXPECT().BlacklistToken(mock.Anything, token, mock.Anything).Return(nil)
	disconnected := make(chan struct{}, 1)
	publisher.EXPECT().DisconnectUser(userID).Run(func(_ uuid.UUID) {
		disconnected <- struct{}{}
	}).Once()

	err = service.Logout(context.Background(), token)
	require.NoError(t, err)

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect user")
	}
}

func TestAuthServiceLogoutReturnsBlacklistError(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	session.EXPECT().BlacklistToken(mock.Anything, "invalid-token", time.Hour).Return(errors.New("redis unavailable"))

	require.Error(t, service.Logout(context.Background(), "invalid-token"))
}

func TestAuthServiceRevokeAllSessionsDelegatesToSessionStore(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	userID := uuid.New()
	session.EXPECT().RevokeAllSessions(mock.Anything, userID).Return(nil)

	require.NoError(t, service.RevokeAllSessions(context.Background(), userID))
}

func TestAuthServiceVerifyUserReturnsUserFromValidToken(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, session, _ := newMockAuthService(t, client)
	ctx := context.Background()
	u := client.User.Create().SetUsername("verified").SetRole(user.RoleUser).SaveX(ctx)
	token, err := helper.GenerateJWT("test-secret", 3600, u.ID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return(false, nil)

	result, err := service.VerifyUser(ctx, token)

	require.NoError(t, err)
	require.Equal(t, u.ID, result.ID)
	require.Equal(t, string(user.RoleUser), result.Role)
}

func TestAuthServiceVerifyUserRejectsRevokedToken(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	uID := uuid.New()
	token, err := helper.GenerateJWT("test-secret", 3600, uID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, uID, mock.AnythingOfType("int64")).Return(true, nil)

	result, err := service.VerifyUser(context.Background(), token)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserReturnsServiceUnavailableWhenRevokeCheckFails(t *testing.T) {
	service, _, _, session, _ := newMockAuthService(t, nil)
	userID := uuid.New()
	token, err := helper.GenerateJWT("test-secret", 3600, userID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, userID, mock.AnythingOfType("int64")).Return(false, errors.New("redis unavailable"))

	result, err := service.VerifyUser(context.Background(), token)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsMalformedToken(t *testing.T) {
	service, _, _, _, _ := newMockAuthService(t, nil)

	result, err := service.VerifyUser(context.Background(), "not-a-jwt")

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsUnexpectedSigningMethod(t *testing.T) {
	service, _, _, _, _ := newMockAuthService(t, nil)
	token := jwt.NewWithClaims(jwt.SigningMethodNone, helper.JWTClaims{UserID: uuid.New()})
	serialized, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	result, err := service.VerifyUser(context.Background(), serialized)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsMissingIssuedAt(t *testing.T) {
	service, _, _, _, _ := newMockAuthService(t, nil)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, helper.JWTClaims{UserID: uuid.New()})
	serialized, err := token.SignedString([]byte("test-secret"))
	require.NoError(t, err)

	result, err := service.VerifyUser(context.Background(), serialized)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsUnknownUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, session, _ := newMockAuthService(t, client)
	userID := uuid.New()
	token, err := helper.GenerateJWT("test-secret", 3600, userID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, userID, mock.AnythingOfType("int64")).Return(false, nil)

	result, err := service.VerifyUser(context.Background(), token)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsTemporarilyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, session, _ := newMockAuthService(t, client)
	ctx := context.Background()
	bannedUntil := time.Now().UTC().Add(time.Hour)
	u := client.User.Create().SetUsername("banned").SetRole(user.RoleUser).SetIsBanned(true).SetBannedUntil(bannedUntil).SaveX(ctx)
	token, err := helper.GenerateJWT("test-secret", 3600, u.ID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return(false, nil)

	result, err := service.VerifyUser(ctx, token)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserRejectsPermanentlyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, session, _ := newMockAuthService(t, client)
	ctx := context.Background()
	u := client.User.Create().SetUsername("banned").SetRole(user.RoleUser).SetIsBanned(true).SaveX(ctx)
	token, err := helper.GenerateJWT("test-secret", 3600, u.ID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return(false, nil)

	result, err := service.VerifyUser(ctx, token)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceVerifyUserLiftsExpiredBan(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, session, _ := newMockAuthService(t, client)
	ctx := context.Background()
	u := client.User.Create().
		SetUsername("expired-ban").
		SetRole(user.RoleUser).
		SetIsBanned(true).
		SetBanReason("old reason").
		SetBannedUntil(time.Now().UTC().Add(-time.Hour)).
		SaveX(ctx)
	token, err := helper.GenerateJWT("test-secret", 3600, u.ID)
	require.NoError(t, err)
	session.EXPECT().IsUserRevoked(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return(false, nil)

	result, err := service.VerifyUser(ctx, token)

	require.NoError(t, err)
	require.Equal(t, u.ID, result.ID)
	updated := client.User.GetX(ctx, u.ID)
	require.False(t, updated.IsBanned)
	require.Nil(t, updated.BannedUntil)
	require.Nil(t, updated.BanReason)
}

func TestAuthServiceLoginReturnsAuthResponse(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, storage := newMockAuthService(t, client)
	ctx := context.Background()
	password, err := helper.HashPassword("StrongPass1!")
	require.NoError(t, err)
	u := client.User.Create().
		SetEmail("user@example.com").
		SetUsername("user").
		SetFullName("Test User").
		SetPasswordHash(password).
		SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)

	result, err := service.Login(ctx, model.LoginRequest{Email: " USER@EXAMPLE.COM ", Password: "StrongPass1!", CaptchaToken: "captcha"})

	require.NoError(t, err)
	require.NotEmpty(t, result.Token)
	require.Equal(t, u.ID, result.User.ID)
	require.Equal(t, "user", result.User.Username)
	storage.AssertNotCalled(t, "GetPublicURL", mock.Anything)
}

func TestAuthServiceLoginMapsAvatarURL(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, storage := newMockAuthService(t, client)
	ctx := context.Background()
	password, err := helper.HashPassword("StrongPass1!")
	require.NoError(t, err)
	avatar := client.Media.Create().
		SetFileName("avatars/login.png").
		SetOriginalName("login.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory("user_avatar").
		SaveX(ctx)
	u := client.User.Create().
		SetEmail("avatar@example.com").
		SetUsername("avatar").
		SetFullName("Avatar User").
		SetPasswordHash(password).
		SetAvatarID(avatar.ID).
		SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)
	storage.EXPECT().GetPublicURL(avatar.FileName).Return("https://cdn/avatar.png")

	result, err := service.Login(ctx, model.LoginRequest{Email: *u.Email, Password: "StrongPass1!", CaptchaToken: "captcha"})

	require.NoError(t, err)
	require.Equal(t, "https://cdn/avatar.png", result.User.Avatar)
}

func TestAuthServiceLoginRejectsCaptchaFailure(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)
	captcha.EXPECT().Verify("captcha", "").Return(errors.New("captcha failed"))

	result, err := service.Login(context.Background(), model.LoginRequest{Email: "user@example.com", Password: "StrongPass1!", CaptchaToken: "captcha"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceLoginRejectsInvalidRequestBeforeCaptcha(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)

	result, err := service.Login(context.Background(), model.LoginRequest{
		Email:        "invalid",
		Password:     "weak",
		CaptchaToken: "",
	})

	require.Nil(t, result)
	require.Error(t, err)
	captcha.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
}

func TestAuthServiceLoginRejectsUnknownUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	captcha.EXPECT().Verify("captcha", "").Return(nil)

	result, err := service.Login(context.Background(), model.LoginRequest{Email: "missing@example.com", Password: "StrongPass1!", CaptchaToken: "captcha"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceLoginRejectsWrongPassword(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()
	password, err := helper.HashPassword("StrongPass1!")
	require.NoError(t, err)
	client.User.Create().SetEmail("user@example.com").SetUsername("user").SetPasswordHash(password).SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)

	result, err := service.Login(ctx, model.LoginRequest{Email: "user@example.com", Password: "WrongPass1!", CaptchaToken: "captcha"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceLoginRejectsPermanentlyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()
	password, err := helper.HashPassword("StrongPass1!")
	require.NoError(t, err)
	client.User.Create().SetEmail("user@example.com").SetUsername("user").SetPasswordHash(password).SetIsBanned(true).SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)

	result, err := service.Login(ctx, model.LoginRequest{Email: "user@example.com", Password: "StrongPass1!", CaptchaToken: "captcha"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceLoginLiftsExpiredBan(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()
	password, err := helper.HashPassword("StrongPass1!")
	require.NoError(t, err)
	u := client.User.Create().
		SetEmail("expired@example.com").
		SetUsername("expired").
		SetFullName("Expired Ban").
		SetPasswordHash(password).
		SetIsBanned(true).
		SetBanReason("old reason").
		SetBannedUntil(time.Now().UTC().Add(-time.Hour)).
		SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)

	result, err := service.Login(ctx, model.LoginRequest{
		Email:        "expired@example.com",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
	require.Equal(t, u.ID, result.User.ID)
	updated := client.User.GetX(ctx, u.ID)
	require.False(t, updated.IsBanned)
	require.Nil(t, updated.BannedUntil)
	require.Nil(t, updated.BanReason)
}

func TestAuthServiceRegisterCreatesUserAndReturnsToken(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	result, err := service.Register(context.Background(), model.RegisterUserRequest{
		Email:        " NEW@EXAMPLE.COM ",
		Username:     "NewUser",
		Code:         "123456",
		FullName:     "New User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.Token)
	require.Equal(t, "new@example.com", result.User.Email)
	require.Equal(t, "newuser", result.User.Username)
	require.Equal(t, "New User", result.User.FullName)
	require.NotEmpty(t, result.User.ID)

	created, err := client.User.Query().Where(user.EmailEQ("new@example.com")).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, result.User.ID, created.ID)
	require.NotEqual(t, "StrongPass1!", created.PasswordHash)
}

func TestAuthServiceRegisterReturnsOTPErrorBeforeStartingTransaction(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	otpErr := errors.New("invalid otp")

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeRegister)).Return(otpErr)

	result, err := service.Register(context.Background(), model.RegisterUserRequest{
		Email:        "new@example.com",
		Username:     "newuser",
		Code:         "123456",
		FullName:     "New User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.Nil(t, result)
	require.ErrorIs(t, err, otpErr)
	count, err := client.User.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestAuthServiceRegisterRejectsInvalidRequestBeforeExternalCalls(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)

	result, err := service.Register(context.Background(), model.RegisterUserRequest{
		Email:        "not-an-email",
		Username:     "ab",
		Code:         "123",
		FullName:     "x",
		Password:     "weak",
		CaptchaToken: "",
	})

	require.Nil(t, result)
	require.Error(t, err)
	captcha.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
}

func TestAuthServiceRegisterRejectsCaptchaFailure(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)
	captcha.EXPECT().Verify("captcha", "").Return(errors.New("captcha failed"))

	result, err := service.Register(context.Background(), model.RegisterUserRequest{
		Email:        "new@example.com",
		Username:     "newuser",
		Code:         "123456",
		FullName:     "New User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceRegisterRejectsExistingEmail(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()
	client.User.Create().
		SetEmail("existing@example.com").
		SetUsername("existing").
		SetFullName("Existing User").
		SetPasswordHash("existing-hash").
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "existing@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	result, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "existing@example.com",
		Username:     "newuser",
		Code:         "123456",
		FullName:     "New User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceRegisterRejectsExistingUsername(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()
	client.User.Create().
		SetEmail("existing@example.com").
		SetUsername("takenuser").
		SetFullName("Existing User").
		SetPasswordHash("existing-hash").
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	result, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "new@example.com",
		Username:     "takenuser",
		Code:         "123456",
		FullName:     "New User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAuthServiceResetPasswordUpdatesPasswordAndRevokesSessions(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	ctx := context.Background()
	oldHash, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	u := client.User.Create().
		SetEmail("reset@example.com").
		SetUsername("resetuser").
		SetFullName("Reset User").
		SetPasswordHash(oldHash).
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "reset@example.com", "123456", string(constant.ModeReset)).Return(nil)
	session.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil)
	session.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return("revoke-marker", nil)

	disconnectDone := make(chan struct{}, 1)
	publisher.EXPECT().DisconnectUser(u.ID).Run(func(uuid.UUID) {
		disconnectDone <- struct{}{}
	}).Once()

	err = service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           " RESET@EXAMPLE.COM ",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})

	require.NoError(t, err)
	select {
	case <-disconnectDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect")
	}

	updated := client.User.GetX(ctx, u.ID)
	require.NotEqual(t, oldHash, updated.PasswordHash)
	require.NotNil(t, updated.PasswordHash)
	require.True(t, helper.CheckPasswordHash("NewPass1!", *updated.PasswordHash))
}

func TestAuthServiceRegisterReturnsConstraintConflictWhenBothExist(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	client.User.Create().
		SetEmail("conflictboth@example.com").
		SetUsername("conflictbothuser").
		SetFullName("Conflict User").
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "conflictboth@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "conflictboth@example.com",
		Username:     "conflictbothuser",
		Code:         "123456",
		FullName:     "Conflict User",
		Password:     "StrongPass1!",
		CaptchaToken: "captcha",
	})

	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceResetPasswordRejectsNotFoundForUnknownUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "missing@example.com", "123456", string(constant.ModeReset)).Return(nil)

	err := service.ResetPassword(context.Background(), model.ResetPasswordRequest{
		Email:           "missing@example.com",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})

	require.Error(t, err)
}

func TestAuthServiceResetPasswordReturnsOTPErrorBeforeStartingTransaction(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	otpErr := errors.New("invalid otp")

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "reset@example.com", "123456", string(constant.ModeReset)).Return(otpErr)

	err := service.ResetPassword(context.Background(), model.ResetPasswordRequest{
		Email:           "reset@example.com",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})

	require.ErrorIs(t, err, otpErr)
}

func TestAuthServiceResetPasswordReturnsCaptchaError(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)
	captcha.EXPECT().Verify("captcha", "").Return(errors.New("captcha failed"))

	err := service.ResetPassword(context.Background(), model.ResetPasswordRequest{
		Email:           "reset@example.com",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})

	require.Error(t, err)
}

func TestAuthServiceResetPasswordRejectsInvalidRequestBeforeCaptcha(t *testing.T) {
	service, captcha, _, _, _ := newMockAuthService(t, nil)

	err := service.ResetPassword(context.Background(), model.ResetPasswordRequest{
		Email:           "invalid",
		Code:            "short",
		Password:        "weak",
		ConfirmPassword: "different",
	})

	require.Error(t, err)
	captcha.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
}

func TestAuthServiceResetPasswordReturnsSessionServiceError(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()
	u := client.User.Create().
		SetEmail("reset@example.com").
		SetUsername("resetuser").
		SetFullName("Reset User").
		SetPasswordHash("old-hash").
		SaveX(ctx)
	sessionErr := errors.New("session unavailable")

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "reset@example.com", "123456", string(constant.ModeReset)).Return(nil)
	session.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, sessionErr)

	err := service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset@example.com",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})

	require.Error(t, err)
}

func TestAuthServiceResetPasswordFailsWhenRevokeAllSessionsAtFails(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()
	u := client.User.Create().
		SetEmail("reset-revoke-fail@example.com").
		SetUsername("resetrevokefail").
		SetFullName("Reset Revoke Fail").
		SetPasswordHash("old-hash").
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "reset-revoke-fail@example.com", "123456", string(constant.ModeReset)).Return(nil)
	session.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil)
	session.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("", errors.New("redis fail"))

	err := service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset-revoke-fail@example.com",
		Code:            "123456",
		Password:        "NewPass1!",
		ConfirmPassword: "NewPass1!",
		CaptchaToken:    "captcha",
	})
	require.Error(t, err)
}

type authGoogleRoundTripper func(*http.Request) (*http.Response, error)

func (f authGoogleRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testGoogleMockClient(t *testing.T, tokenStatus int, tokenBody string, userinfoStatus int, userinfoBody string) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: authGoogleRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "token") || strings.Contains(req.URL.Path, "oauth2/v4/token") || req.URL.Host == "oauth2.googleapis.com" {
				if tokenStatus == 0 {
					return nil, errors.New("network error on token")
				}
				return &http.Response{
					StatusCode: tokenStatus,
					Body:       io.NopCloser(strings.NewReader(tokenBody)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			if userinfoStatus == 0 {
				return nil, errors.New("network error on userinfo")
			}
			return &http.Response{
				StatusCode: userinfoStatus,
				Body:       io.NopCloser(strings.NewReader(userinfoBody)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}
}

func TestAuthServiceGoogleExchangeRegistersNewUserWithAvatar(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, storage := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-999","email":"newgoogle@example.com","verified_email":true,"name":"New Google User","picture":"https://cdn/google-avatar.png"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	payloadBytes, err := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	require.NoError(t, err)

	validState := "valid-state-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+validState).Return(string(payloadBytes), nil)
	storage.EXPECT().Download("https://cdn/google-avatar.png").Return([]byte("fake-image"), "image/png", nil)
	storage.EXPECT().StoreFromReader(mock.Anything, "image/png", mock.Anything, true).Return(nil)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.example/avatar.png")

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		State: validState,
		Code:  "auth-code",
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "newgoogle@example.com", res.User.Email)
	require.Equal(t, "New Google User", res.User.FullName)
	require.Equal(t, "https://cdn.example/avatar.png", res.User.Avatar)
	require.NotEmpty(t, res.Token)
}

func TestAuthServiceGoogleExchangeLogsInExistingUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, storage := newMockAuthService(t, client)

	ctx := context.Background()
	existingUser := client.User.Create().
		SetEmail("existinggoogle@example.com").
		SetUsername("existinggoogle").
		SetFullName("Existing Google").
		SaveX(ctx)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-888","email":"existinggoogle@example.com","verified_email":true,"name":"Existing Google"}`,
	)
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	payloadBytes, err := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	require.NoError(t, err)

	validState := "valid-state-2-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+validState).Return(string(payloadBytes), nil)

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		State: validState,
		Code:  "auth-code",
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, existingUser.ID, res.User.ID)
	require.Equal(t, "existinggoogle@example.com", res.User.Email)
	_ = storage
}

func TestAuthServiceGoogleExchangeRejectsUnverifiedEmail(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-777","email":"unverified@example.com","verified_email":false,"name":"Unverified"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	payloadBytes, err := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	require.NoError(t, err)

	unverifiedState := "unverified-state-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+unverifiedState).Return(string(payloadBytes), nil)

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		State: unverifiedState,
		Code:  "auth-code",
	})

	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceGoogleExchangeRejectsMissingEmailOrSub(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	httpClient1 := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-666","email":"","verified_email":true,"name":"No Email"}`,
	)
	ctx1 := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient1)

	state1 := "no-email-state-1234567890"
	payloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state1).Return(string(payloadBytes), nil)

	res1, err1 := service.GoogleExchange(ctx1, model.GoogleLoginRequest{State: state1, Code: "code"})
	require.Nil(t, res1)
	require.Error(t, err1)

	httpClient2 := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"","email":"submissing@example.com","verified_email":true,"name":"No Sub"}`,
	)
	ctx2 := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient2)

	state2 := "no-sub-state-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state2).Return(string(payloadBytes), nil)

	res2, err2 := service.GoogleExchange(ctx2, model.GoogleLoginRequest{State: state2, Code: "code"})
	require.Nil(t, res2)
	require.Error(t, err2)
}

func TestAuthServiceGoogleExchangeRejectsPermanentlyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	ctx := context.Background()
	client.User.Create().
		SetEmail("permban@example.com").
		SetUsername("permbanuser").
		SetFullName("Perm Ban").
		SetIsBanned(true).
		SaveX(ctx)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-555","email":"permban@example.com","verified_email":true,"name":"Perm Ban"}`,
	)
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	state := "perm-ban-state-1234567890"
	payloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state).Return(string(payloadBytes), nil)

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{State: state, Code: "code"})
	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceGoogleExchangeLiftsExpiredBan(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	ctx := context.Background()
	expired := time.Now().UTC().Add(-time.Hour)
	client.User.Create().
		SetEmail("expiredban@example.com").
		SetUsername("expiredbanuser").
		SetFullName("Expired Ban").
		SetIsBanned(true).
		SetBannedUntil(expired).
		SaveX(ctx)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-444","email":"expiredban@example.com","verified_email":true,"name":"Expired Ban"}`,
	)
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	state := "expired-ban-state-1234567890"
	payloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state).Return(string(payloadBytes), nil)

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{State: state, Code: "code"})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestAuthServiceGoogleExchangeHandlesExternalErrors(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	payloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})

	tokenErrClient := testGoogleMockClient(t, http.StatusBadRequest, `{"error":"invalid_grant"}`, 0, "")
	ctx1 := context.WithValue(context.Background(), oauth2.HTTPClient, tokenErrClient)
	state1 := "token-err-state-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state1).Return(string(payloadBytes), nil)

	res1, err1 := service.GoogleExchange(ctx1, model.GoogleLoginRequest{State: state1, Code: "bad-code"})
	require.Nil(t, res1)
	require.Error(t, err1)

	userinfoErrClient := testGoogleMockClient(t, http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`, http.StatusInternalServerError, `{"error":"internal"}`)
	ctx2 := context.WithValue(context.Background(), oauth2.HTTPClient, userinfoErrClient)
	state2 := "userinfo-err-state-1234567890"
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state2).Return(string(payloadBytes), nil)

	res, err := service.GoogleExchange(ctx2, model.GoogleLoginRequest{State: state2, Code: "code"})
	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceRegisterHandlesUsernameConstraintConflict(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	client.User.Create().SetEmail("existing@example.com").SetUsername("takenuser").SetPasswordHash("hash").SaveX(ctx)

	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeRegister)).Return(nil).Once()

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "new@example.com",
		Username:     "takenuser",
		FullName:     "New User",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "cap-token",
	})
	require.Nil(t, res)
	require.Error(t, err)
	var httpErr *helper.AppError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusConflict, httpErr.Code)
	require.Equal(t, "Username already taken", httpErr.Message)
}

func TestAuthServiceRegisterHandlesSoftDeletedConflictFallback(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	client.User.Create().SetEmail("softdeleted@example.com").SetUsername("softdeleted").SetDeletedAt(time.Now().UTC()).SetPasswordHash("hash").SaveX(ctx)

	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "softdeleted@example.com", "123456", string(constant.ModeRegister)).Return(nil).Once()

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "softdeleted@example.com",
		Username:     "softdeleted",
		FullName:     "Soft Deleted",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "cap-token",
	})
	require.Nil(t, res)
	require.Error(t, err)
	var httpErr *helper.AppError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusConflict, httpErr.Code)
	require.Equal(t, "Email or Username already taken", httpErr.Message)
}

func TestAuthServiceRegisterRejectsInvalidFullName(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "emptyname@example.com",
		Username:     "emptyname",
		FullName:     "  ",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "cap-token",
	})
	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceResetPasswordDisconnectsUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	ctx := context.Background()
	u := client.User.Create().SetEmail("reset-disconnect@example.com").SetUsername("resetdisc").SetPasswordHash("hash").SaveX(ctx)

	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "reset-disconnect@example.com", "123456", string(constant.ModeReset)).Return(nil).Once()
	session.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	session.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return("revoke-marker", nil).Once()

	done := make(chan struct{}, 1)
	publisher.EXPECT().DisconnectUser(u.ID).Run(func(uuid.UUID) {
		done <- struct{}{}
	}).Once()

	err := service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset-disconnect@example.com",
		Code:            "123456",
		Password:        "NewPassword123!",
		ConfirmPassword: "NewPassword123!",
		CaptchaToken:    "cap-token",
	})
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected disconnect call")
	}
}

func TestAuthServiceLoginRejectsTemporarilyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()

	hash, _ := helper.HashPassword("Password123!")
	bannedUntil := time.Now().Add(time.Hour)
	client.User.Create().
		SetEmail("tempban-login@example.com").
		SetUsername("tempbanlogin").
		SetPasswordHash(hash).
		SetIsBanned(true).
		SetBannedUntil(bannedUntil).
		SaveX(ctx)

	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()

	res, err := service.Login(ctx, model.LoginRequest{
		Email:        "tempban-login@example.com",
		Password:     "Password123!",
		CaptchaToken: "cap-token",
	})
	require.Nil(t, res)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Account suspended until")
}

func TestAuthServiceLoginHandlesNilUsernameAndFullName(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()

	hash, _ := helper.HashPassword("Password123!")
	client.User.Create().
		SetEmail("nilfields-login@example.com").
		SetPasswordHash(hash).
		SaveX(ctx)

	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()

	res, err := service.Login(ctx, model.LoginRequest{
		Email:        "nilfields-login@example.com",
		Password:     "Password123!",
		CaptchaToken: "cap-token",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Empty(t, res.User.Username)
	require.Empty(t, res.User.FullName)
}

func TestAuthServiceGoogleExchangeRegistersNewUserWithoutPicture(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisAdapter, _, _ := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-nopict","email":"new-nopict@example.com","verified_email":true,"name":"No Picture User"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	statePayload := `{"code_verifier":"verifier"}`
	redisAdapter.EXPECT().GetDel(mock.Anything, "oauth:google:state:test-state-nopict").Return(statePayload, nil).Once()

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "auth-code",
		State: "test-state-nopict",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "new-nopict@example.com", res.User.Email)
	require.Equal(t, "No Picture User", res.User.FullName)
	require.Empty(t, res.User.Avatar)
}

func TestAuthServiceGoogleExchangeRegistersNewUserWhenDownloadFails(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisAdapter, _, storage := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-dlfail","email":"new-dlfail@example.com","verified_email":true,"name":"Download Fail User","picture":"https://google.test/fail.jpg"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	statePayload := `{"code_verifier":"verifier"}`
	redisAdapter.EXPECT().GetDel(mock.Anything, "oauth:google:state:test-state-dlfail").Return(statePayload, nil).Once()
	storage.EXPECT().Download("https://google.test/fail.jpg").Return(nil, "", errors.New("download failed")).Once()

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "auth-code",
		State: "test-state-dlfail",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "new-dlfail@example.com", res.User.Email)
	require.Empty(t, res.User.Avatar)
}

func TestAuthServiceGoogleExchangeRegistersNewUserWhenStoreFromReaderFails(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisAdapter, _, storage := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-storefail","email":"new-storefail@example.com","verified_email":true,"name":"Store Fail User","picture":"https://google.test/storefail.jpg"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	statePayload := `{"code_verifier":"verifier"}`
	redisAdapter.EXPECT().GetDel(mock.Anything, "oauth:google:state:test-state-storefail").Return(statePayload, nil).Once()
	storage.EXPECT().Download("https://google.test/storefail.jpg").Return([]byte("imagebytes"), "image/jpeg", nil).Once()
	storage.EXPECT().StoreFromReader(mock.Anything, "image/jpeg", mock.Anything, true).Return(errors.New("s3 upload failed")).Once()

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "auth-code",
		State: "test-state-storefail",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "new-storefail@example.com", res.User.Email)
	require.Empty(t, res.User.Avatar)
}

func TestAuthServiceGoogleExchangeRejectsTemporarilyBannedUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisAdapter, _, _ := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-tempban","email":"tempban-google@example.com","verified_email":true,"name":"Temp Banned"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	bannedUntil := time.Now().Add(time.Hour)
	client.User.Create().
		SetEmail("tempban-google@example.com").
		SetUsername("tempbangoogle").
		SetIsBanned(true).
		SetBannedUntil(bannedUntil).
		SaveX(ctx)

	statePayload := `{"code_verifier":"verifier"}`
	redisAdapter.EXPECT().GetDel(mock.Anything, "oauth:google:state:test-state-tempban").Return(statePayload, nil).Once()

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "auth-code",
		State: "test-state-tempban",
	})
	require.Nil(t, res)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Account suspended until")
}

func TestAuthServiceGoogleExchangeLinksIdentityToExistingUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisAdapter, _, storage := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-link-123","email":"existing-link@example.com","verified_email":true,"name":"Existing Link User"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	u := client.User.Create().
		SetEmail("existing-link@example.com").
		SetUsername("existinglink").
		SetFullName("Existing Link User").
		SaveX(ctx)

	media := client.Media.Create().
		SetFileName("existing-avatar.jpg").
		SetOriginalName("orig.jpg").
		SetFileSize(100).
		SetMimeType("image/jpeg").
		SetCategory(media.CategoryUserAvatar).
		SetUploader(u).
		SaveX(ctx)

	client.User.UpdateOne(u).SetAvatarID(media.ID).SaveX(ctx)

	statePayload := `{"code_verifier":"verifier"}`
	redisAdapter.EXPECT().GetDel(mock.Anything, "oauth:google:state:test-state-link-1234").Return(statePayload, nil).Once()
	storage.EXPECT().GetPublicURL("existing-avatar.jpg").Return("https://cdn.test/existing-avatar.jpg").Once()

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{
		Code:  "auth-code",
		State: "test-state-link-1234",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "existing-link@example.com", res.User.Email)
	require.Equal(t, "https://cdn.test/existing-avatar.jpg", res.User.Avatar)
}

func TestAuthServiceValidationErrors(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, redisMock, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	_, err := service.Login(ctx, model.LoginRequest{
		Email: "not-an-email",
	})
	require.Error(t, err)

	_, err = service.Register(ctx, model.RegisterUserRequest{
		Email: "not-an-email",
	})
	require.Error(t, err)

	err = service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email: "not-an-email",
	})
	require.Error(t, err)

	redisMock.EXPECT().Set(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	initRes, err := service.BeginGoogleAuth(ctx)
	require.NoError(t, err)
	require.NotNil(t, initRes)

	u := client.User.Create().SetEmail("reset-fail-rev@example.com").SetUsername("failrev").SetPasswordHash("hash").SaveX(ctx)
	captcha.EXPECT().Verify("cap-token", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "reset-fail-rev@example.com", "123456", string(constant.ModeReset)).Return(nil).Once()
	session.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("redis dead")).Once()

	err = service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset-fail-rev@example.com",
		Password:        "Password123!",
		ConfirmPassword: "Password123!",
		Code:            "123456",
		CaptchaToken:    "cap-token",
	})
	require.Error(t, err)
}

func TestAuthServiceCancelledContextBranches(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, redisMock, session, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	captcha.EXPECT().Verify(mock.Anything, mock.Anything).Return(nil).Maybe()
	otp.EXPECT().VerifyOTP(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	redisMock.EXPECT().Set(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	session.EXPECT().BlacklistToken(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	session.EXPECT().IsUserRevoked(mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	_, err := service.Login(ctx, model.LoginRequest{
		Email:        "cancel@example.com",
		Password:     "Password123!",
		CaptchaToken: "valid-captcha",
	})
	require.Error(t, err)

	_, err = service.Register(ctx, model.RegisterUserRequest{
		Email:        "cancel@example.com",
		Username:     "canceluser",
		FullName:     "Cancel User",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "valid-captcha",
	})
	require.Error(t, err)

	err = service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "cancel@example.com",
		Password:        "Password123!",
		ConfirmPassword: "Password123!",
		Code:            "123456",
		CaptchaToken:    "valid-captcha",
	})
	require.Error(t, err)

	_, err = service.VerifyUser(ctx, "invalid.token.string")
	require.Error(t, err)
}

func TestAuthServiceRegisterWithEmptyFullName(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp

	captcha.EXPECT().Verify("valid-captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "nofullname@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	res, err := service.Register(context.Background(), model.RegisterUserRequest{
		Email:        "nofullname@example.com",
		Username:     "nofullname",
		FullName:     "No Full Name",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "valid-captcha",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "No Full Name", res.User.FullName)
}

func TestAuthServiceBeginGoogleAuthWithoutFingerprint(t *testing.T) {
	service, _, redisMock, _, _ := newMockAuthService(t, nil)
	ctx := context.Background()

	redisMock.EXPECT().Set(mock.Anything, mock.Anything, mock.MatchedBy(func(value interface{}) bool {
		payload, ok := value.([]byte)
		return ok && !strings.Contains(string(payload), "fingerprint_hash")
	}), googleOAuthStateTTL).Return(nil).Once()

	res, err := service.BeginGoogleAuth(ctx)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.AuthURL)
	require.NotEmpty(t, res.State)
}

func TestAuthServiceResetPasswordRejectsInvalidConfirmPassword(t *testing.T) {
	service, _, _, _, _ := newMockAuthService(t, nil)

	err := service.ResetPassword(context.Background(), model.ResetPasswordRequest{
		Email:           "reset@example.com",
		Code:            "123456",
		Password:        "Password123!",
		ConfirmPassword: "DifferentPassword123!",
		CaptchaToken:    "valid-captcha",
	})
	require.Error(t, err)
}

func TestAuthServiceBeginGoogleAuthFailsOnRedisError(t *testing.T) {
	service, _, redisMock, _, _ := newMockAuthService(t, nil)
	ctx := context.Background()

	redisMock.EXPECT().Set(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("redis down")).Once()

	res, err := service.BeginGoogleAuth(ctx)
	require.Nil(t, res)
	require.Error(t, err)
}

func TestAuthServiceGoogleExchangeWithShortAndLongBaseUsernames(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, _ := newMockAuthService(t, client)

	shortHTTPClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-short-123","email":"ab@example.com","verified_email":true,"name":"Short User"}`,
	)
	shortCtx := context.WithValue(context.Background(), oauth2.HTTPClient, shortHTTPClient)
	shortState := "short-state-1234567890"
	shortPayloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+shortState).Return(string(shortPayloadBytes), nil)

	shortRes, err := service.GoogleExchange(shortCtx, model.GoogleLoginRequest{State: shortState, Code: "code"})
	require.NoError(t, err)
	require.NotNil(t, shortRes)
	require.Contains(t, shortRes.User.Username, "userab")

	longHTTPClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-long-123","email":"thisisaverylongemailaddresswithmorethanfortycharactersbeforetheat@example.com","verified_email":true,"name":"Long User"}`,
	)
	longCtx := context.WithValue(context.Background(), oauth2.HTTPClient, longHTTPClient)
	longState := "long-state-1234567890"
	longPayloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+longState).Return(string(longPayloadBytes), nil)

	longRes, err := service.GoogleExchange(longCtx, model.GoogleLoginRequest{State: longState, Code: "code"})
	require.NoError(t, err)
	require.NotNil(t, longRes)
	require.LessOrEqual(t, len(longRes.User.Username), 44)
}

func TestAuthServiceGoogleExchangeWithEmptyContentTypeDownload(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, redisStore, _, storage := newMockAuthService(t, client)

	httpClient := testGoogleMockClient(t,
		http.StatusOK, `{"access_token":"mock-token","token_type":"Bearer","expires_in":3600}`,
		http.StatusOK, `{"id":"sub-pic-empty","email":"picempty@example.com","verified_email":true,"name":"Pic User","picture":"https://pic.test/photo.jpg"}`,
	)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	state := "pic-empty-state-12345"
	payloadBytes, _ := json.Marshal(googleOAuthStatePayload{CodeVerifier: "test-verifier"})
	redisStore.EXPECT().GetDel(mock.Anything, "oauth:google:state:"+state).Return(string(payloadBytes), nil)

	storage.EXPECT().Download("https://pic.test/photo.jpg").Return([]byte("fake-image"), "", nil)
	storage.EXPECT().StoreFromReader(mock.Anything, "image/jpeg", mock.Anything, true).Return(nil)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.test/avatar.jpg")

	res, err := service.GoogleExchange(ctx, model.GoogleLoginRequest{State: state, Code: "code"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.test/avatar.jpg", res.User.Avatar)
}

func TestAuthServiceRegisterSoftDeletedBothEmailAndUsernameReturnsGenericConflict(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	now := time.Now().UTC()
	client.User.Create().
		SetEmail("bothdeleted@example.com").
		SetUsername("bothdeleted").
		SetPasswordHash("hash").
		SetDeletedAt(now).
		SaveX(ctx)

	captcha.EXPECT().Verify("valid-captcha", "").Return(nil)
	otp.EXPECT().VerifyOTP(mock.Anything, "bothdeleted@example.com", "123456", string(constant.ModeRegister)).Return(nil)

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "bothdeleted@example.com",
		Username:     "bothdeleted",
		FullName:     "Both Deleted",
		Password:     "Password123!",
		Code:         "123456",
		CaptchaToken: "valid-captcha",
	})
	require.Nil(t, res)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already taken")
}

func TestAuthServiceRegisterAndResetValidationBranches(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, _, _, _, _ := newMockAuthService(t, client)
	ctx := context.Background()

	_, err := service.Register(ctx, model.RegisterUserRequest{
		Email: "invalid-email",
	})
	require.Error(t, err)

	err = service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email: "invalid-email",
	})
	require.Error(t, err)
}

func TestAuthServiceResetPasswordFailsRevokeAllSessionsAt(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, sessionStore, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	u := client.User.Create().
		SetEmail("reset-fail-revoke@example.com").
		SetUsername("resetfailrev").
		SetPasswordHash("hash").
		SaveX(ctx)

	captcha.EXPECT().Verify("valid-captcha", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "reset-fail-revoke@example.com", "123456", string(constant.ModeReset)).Return(nil).Once()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("", errors.New("redis error")).Once()

	err := service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset-fail-revoke@example.com",
		Code:            "123456",
		Password:        "NewPassword123!",
		ConfirmPassword: "NewPassword123!",
		CaptchaToken:    "valid-captcha",
	})
	require.Error(t, err)
}

func TestAuthServiceResetPasswordWithWsHubDisconnectsUser(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, sessionStore, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	publisher := websocketmocks.NewMockPublisher(t)
	disconnected := make(chan uuid.UUID, 1)

	ctx := context.Background()

	u := client.User.Create().
		SetEmail("reset-disconnect@example.com").
		SetUsername("resetdisc").
		SetPasswordHash("hash").
		SaveX(ctx)

	publisher.EXPECT().DisconnectUser(u.ID).Run(func(id uuid.UUID) {
		disconnected <- id
	}).Once()

	service.wsHub = publisher

	captcha.EXPECT().Verify("valid-captcha", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "reset-disconnect@example.com", "123456", string(constant.ModeReset)).Return(nil).Once()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("marker", nil).Once()

	err := service.ResetPassword(ctx, model.ResetPasswordRequest{
		Email:           "reset-disconnect@example.com",
		Code:            "123456",
		Password:        "NewPassword123!",
		ConfirmPassword: "NewPassword123!",
		CaptchaToken:    "valid-captcha",
	})
	require.NoError(t, err)

	select {
	case id := <-disconnected:
		require.Equal(t, u.ID, id)
	case <-time.After(2 * time.Second):
		t.Fatal("expected disconnect user broadcast")
	}
}

func TestAuthServiceRegisterConstraintConflictReturnsUsernameTaken(t *testing.T) {
	client := newAuthTestClient(t)
	defer func() { _ = client.Close() }()
	service, captcha, _, _, _ := newMockAuthService(t, client)
	otp := servicemocks.NewMockauthOTP(t)
	service.otpService = otp
	ctx := context.Background()

	client.User.Create().
		SetEmail("different@example.com").
		SetUsername("takenuser").
		SaveX(ctx)

	captcha.EXPECT().Verify("captcha", "").Return(nil).Once()
	otp.EXPECT().VerifyOTP(mock.Anything, "fresh@example.com", "123456", string(constant.ModeRegister)).Return(nil).Once()

	res, err := service.Register(ctx, model.RegisterUserRequest{
		Email:        "fresh@example.com",
		Username:     "takenuser",
		FullName:     "Fresh User",
		Password:     "NewPass1!",
		Code:         "123456",
		CaptchaToken: "captcha",
	})
	require.Error(t, err)
	require.Nil(t, res)
	require.Contains(t, err.Error(), "Username already taken")
}
