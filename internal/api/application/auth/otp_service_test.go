package auth

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	repositorymocks "AtoiTalkAPI/internal/infrastructure/database/repository/mocks"
	adaptermocks "AtoiTalkAPI/internal/infrastructure/mocks"
	redisinfrastructure "AtoiTalkAPI/internal/infrastructure/redis"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newOTPService(t *testing.T, email otpEmailSender, captcha otpCaptchaVerifier, redisStore otpStore, limiter otpRateLimiter) (*OTPService, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:otp-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	service := NewOTPService(
		client,
		&config.AppConfig{OTPSecret: "secret", OTPExp: 300, OTPRateLimitSeconds: 60},
		config.NewValidator(),
		email,
		captcha,
		redisStore,
		limiter,
	)
	return service, client
}

func TestOTPServiceSendOTPStoresAndSendsCode(t *testing.T) {
	email := adaptermocks.NewMockEmailSender(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	limiter.EXPECT().Allow(mock.Anything, "ratelimit:otp:register:person@example.com:global", 1, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().Set(mock.Anything, "otp:register:person@example.com", mock.Anything, 5*time.Minute).Return(nil)
	email.EXPECT().Send([]string{"person@example.com"}, "Your OTP Code", mock.Anything).Return(nil)

	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()

	err := service.SendOTP(context.Background(), model.SendOTPRequest{
		Email:        "Person@Example.com",
		Mode:         "register",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
}

func TestOTPServiceSendOTPUsesFingerprintScopeAndDeletesStoredCodeWhenEmailFails(t *testing.T) {
	email := adaptermocks.NewMockEmailSender(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	fingerprint := "client-fingerprint"
	scope := helper.HashOTP(fingerprint, "secret")

	captcha.EXPECT().Verify("captcha", "").Return(nil)
	limiter.EXPECT().Allow(mock.Anything, "ratelimit:otp:register:person@example.com:"+scope, 1, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().Set(mock.Anything, "otp:register:person@example.com", mock.Anything, 5*time.Minute).Return(nil)
	email.EXPECT().Send([]string{"person@example.com"}, "Your OTP Code", mock.Anything).Return(errors.New("smtp unavailable"))
	store.EXPECT().ConsumeIfMatch(mock.Anything, "otp:register:person@example.com", mock.Anything).Return(true, nil)

	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()

	err := service.SendOTP(helper.WithClientFingerprint(context.Background(), fingerprint), model.SendOTPRequest{
		Email:        "person@example.com",
		Mode:         "register",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
}

func TestOTPServiceFailedOlderEmailDoesNotDeleteNewerOTP(t *testing.T) {
	server := miniredis.RunT(t)
	store, err := redisinfrastructure.NewRedisAdapter(&config.AppConfig{RedisHost: server.Host(), RedisPort: server.Port()})
	require.NoError(t, err)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	captcha.EXPECT().Verify("captcha", "").Return(nil).Twice()
	limiter := repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 1, time.Minute).Return(true, time.Minute, nil).Twice()
	email := adaptermocks.NewMockEmailSender(t)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var sends atomic.Int32
	email.EXPECT().Send(mock.Anything, "Your OTP Code", mock.Anything).RunAndReturn(func(_ []string, _, _ string) error {
		if sends.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return errors.New("smtp unavailable")
		}
		return nil
	}).Twice()
	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()
	req := model.SendOTPRequest{Email: "person@example.com", Mode: "register", CaptchaToken: "captcha"}
	firstResult := make(chan error, 1)
	go func() { firstResult <- service.SendOTP(context.Background(), req) }()
	<-firstStarted
	require.NoError(t, service.SendOTP(context.Background(), req))
	key := "otp:register:person@example.com"
	require.True(t, server.Exists(key))
	close(releaseFirst)
	require.NoError(t, <-firstResult)
	require.True(t, server.Exists(key), "a failed older delivery must not delete the newer code")
}

func TestOTPServiceSendOTPRejectsInvalidRequestAndCaptcha(t *testing.T) {
	email := adaptermocks.NewMockEmailSender(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()

	err := service.SendOTP(context.Background(), model.SendOTPRequest{Email: "bad", Mode: "register"})
	require.Error(t, err)

	captcha.EXPECT().Verify("bad", "").Return(errors.New("captcha failed"))
	err = service.SendOTP(context.Background(), model.SendOTPRequest{Email: "person@example.com", Mode: "register", CaptchaToken: "bad"})
	require.Error(t, err)
}

func TestOTPServiceSendOTPRateLimitAndStorageErrors(t *testing.T) {
	tests := []struct {
		name       string
		allow      bool
		limitError error
		storeError error
	}{
		{name: "rejected", allow: false},
		{name: "rate limit error", allow: true, limitError: errors.New("rate limit unavailable")},
		{name: "storage error", allow: true, storeError: errors.New("redis unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := adaptermocks.NewMockEmailSender(t)
			captcha := adaptermocks.NewMockCaptchaVerifier(t)
			store := adaptermocks.NewMockRedisStore(t)
			limiter := repositorymocks.NewMockRateLimiter(t)
			captcha.EXPECT().Verify("ok", "").Return(nil)
			limiter.EXPECT().Allow(mock.Anything, mock.Anything, 1, time.Minute).Return(tt.allow, 25*time.Second, tt.limitError)
			if tt.storeError != nil {
				store.EXPECT().Set(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(tt.storeError)
			}

			service, client := newOTPService(t, email, captcha, store, limiter)
			defer client.Close()
			err := service.SendOTP(context.Background(), model.SendOTPRequest{Email: "person@example.com", Mode: "register", CaptchaToken: "ok"})
			require.Error(t, err)
		})
	}
}

func TestOTPServiceSendOTPSuppressesKnownUserModes(t *testing.T) {
	email := adaptermocks.NewMockEmailSender(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	captcha.EXPECT().Verify("ok", "").Return(nil).Times(2)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 1, time.Minute).Return(true, time.Minute, nil).Times(2)

	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()
	_, err := client.User.Create().SetEmail("person@example.com").Save(context.Background())
	require.NoError(t, err)

	for _, mode := range []string{"register", "change_email"} {
		err = service.SendOTP(context.Background(), model.SendOTPRequest{Email: "person@example.com", Mode: mode, CaptchaToken: "ok"})
		require.NoError(t, err)
	}

	resetEmail := adaptermocks.NewMockEmailSender(t)
	resetCaptcha := adaptermocks.NewMockCaptchaVerifier(t)
	resetStore := adaptermocks.NewMockRedisStore(t)
	resetLimiter := repositorymocks.NewMockRateLimiter(t)
	resetCaptcha.EXPECT().Verify("ok", "").Return(nil)
	resetLimiter.EXPECT().Allow(mock.Anything, mock.Anything, 1, time.Minute).Return(true, time.Minute, nil)
	service, client = newOTPService(t, resetEmail, resetCaptcha, resetStore, resetLimiter)
	defer client.Close()
	err = service.SendOTP(context.Background(), model.SendOTPRequest{Email: "missing@example.com", Mode: "reset", CaptchaToken: "ok"})
	require.NoError(t, err)
}

func TestOTPServiceVerifyOTPBranches(t *testing.T) {
	tests := []struct {
		name         string
		consumeError error
		allow        bool
		allowErr     error
	}{
		{name: "wrong code", allow: true},
		{name: "expired", allow: true},
		{name: "redis error", consumeError: errors.New("redis unavailable"), allow: true},
		{name: "rate limited", allow: false},
		{name: "rate limit error", allow: true, allowErr: errors.New("rate limit unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := adaptermocks.NewMockEmailSender(t)
			captcha := adaptermocks.NewMockCaptchaVerifier(t)
			store := adaptermocks.NewMockRedisStore(t)
			limiter := repositorymocks.NewMockRateLimiter(t)
			limiter.EXPECT().Allow(mock.Anything, "ratelimit:otp_verify:reset:person@example.com:global", 5, time.Minute).Return(tt.allow, 12*time.Second, tt.allowErr)
			if tt.allow && tt.allowErr == nil {
				store.EXPECT().ConsumeIfMatch(mock.Anything, "otp:reset:person@example.com", helper.HashOTP("123456", "secret")).Return(false, tt.consumeError)
			}

			service, client := newOTPService(t, email, captcha, store, limiter)
			defer client.Close()
			err := service.VerifyOTP(context.Background(), "Person@Example.com", "123456", "reset")
			require.Error(t, err)
		})
	}
}

func TestOTPServiceVerifyOTPSuccessAndStorageError(t *testing.T) {
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 5, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().ConsumeIfMatch(mock.Anything, "otp:reset:person@example.com", helper.HashOTP("123456", "secret")).Return(true, nil)
	service, client := newOTPService(t, adaptermocks.NewMockEmailSender(t), adaptermocks.NewMockCaptchaVerifier(t), store, limiter)
	defer client.Close()

	err := service.VerifyOTP(context.Background(), "person@example.com", "123456", "reset")
	require.NoError(t, err)

	store = adaptermocks.NewMockRedisStore(t)
	limiter = repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 5, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().ConsumeIfMatch(mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("redis unavailable"))
	service, client = newOTPService(t, adaptermocks.NewMockEmailSender(t), adaptermocks.NewMockCaptchaVerifier(t), store, limiter)
	defer client.Close()
	err = service.VerifyOTP(context.Background(), "person@example.com", "123456", "reset")
	require.Error(t, err)
}

func TestOTPServiceVerifyOTPConcurrentRequestsConsumeOnce(t *testing.T) {
	email := adaptermocks.NewMockEmailSender(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 5, time.Minute).Return(true, time.Minute, nil).Times(2)
	store.EXPECT().ConsumeIfMatch(mock.Anything, "otp:reset:person@example.com", helper.HashOTP("123456", "secret")).Return(true, nil).Once()
	store.EXPECT().ConsumeIfMatch(mock.Anything, "otp:reset:person@example.com", helper.HashOTP("123456", "secret")).Return(false, nil).Once()
	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- service.VerifyOTP(context.Background(), "person@example.com", "123456", "reset") }()
	}
	first, second := <-results, <-results
	require.Equal(t, 1, boolToInt(first == nil)+boolToInt(second == nil), "OTP may only be consumed once")
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestOTPServiceClientRateLimitScopeDefaultsToGlobal(t *testing.T) {
	service, client := newOTPService(t, nil, nil, nil, nil)
	defer client.Close()

	require.Equal(t, "global", service.clientRateLimitScope(context.Background()))
}

func TestOTPServiceClientRateLimitScopeHashesFingerprint(t *testing.T) {
	service, client := newOTPService(t, nil, nil, nil, nil)
	defer client.Close()
	fingerprint := "client-fingerprint"

	require.Equal(t, helper.HashOTP(fingerprint, "secret"), service.clientRateLimitScope(helper.WithClientFingerprint(context.Background(), fingerprint)))
}
