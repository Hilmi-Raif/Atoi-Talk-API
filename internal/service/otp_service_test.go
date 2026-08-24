package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/enttest"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
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
	store.EXPECT().Del(mock.Anything, "otp:register:person@example.com").Return(nil)

	service, client := newOTPService(t, email, captcha, store, limiter)
	defer client.Close()

	err := service.SendOTP(helper.WithClientFingerprint(context.Background(), fingerprint), model.SendOTPRequest{
		Email:        "person@example.com",
		Mode:         "register",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
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
		name     string
		value    string
		getError error
		allow    bool
		allowErr error
	}{
		{name: "wrong code", value: "wrong", allow: true},
		{name: "expired", getError: redis.Nil, allow: true},
		{name: "redis error", getError: errors.New("redis unavailable"), allow: true},
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
				store.EXPECT().Get(mock.Anything, "otp:reset:person@example.com").Return(tt.value, tt.getError)
			}

			service, client := newOTPService(t, email, captcha, store, limiter)
			defer client.Close()
			err := service.VerifyOTP(context.Background(), "Person@Example.com", "123456", "reset")
			require.Error(t, err)
		})
	}
}

func TestOTPServiceVerifyOTPSuccessAndDeleteError(t *testing.T) {
	store := adaptermocks.NewMockRedisStore(t)
	limiter := repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 5, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().Get(mock.Anything, "otp:reset:person@example.com").Return(helper.HashOTP("123456", "secret"), nil)
	store.EXPECT().Del(mock.Anything, "otp:reset:person@example.com").Return(nil)
	service, client := newOTPService(t, adaptermocks.NewMockEmailSender(t), adaptermocks.NewMockCaptchaVerifier(t), store, limiter)
	defer client.Close()

	err := service.VerifyOTP(context.Background(), "person@example.com", "123456", "reset")
	require.NoError(t, err)

	store = adaptermocks.NewMockRedisStore(t)
	limiter = repositorymocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything, mock.Anything, 5, time.Minute).Return(true, time.Minute, nil)
	store.EXPECT().Get(mock.Anything, mock.Anything).Return(helper.HashOTP("123456", "secret"), nil)
	store.EXPECT().Del(mock.Anything, mock.Anything).Return(errors.New("delete failed"))
	service, client = newOTPService(t, adaptermocks.NewMockEmailSender(t), adaptermocks.NewMockCaptchaVerifier(t), store, limiter)
	defer client.Close()
	err = service.VerifyOTP(context.Background(), "person@example.com", "123456", "reset")
	require.Error(t, err)
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
