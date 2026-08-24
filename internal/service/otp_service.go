package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/constant"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"context"
	"crypto/rand"
	"embed"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/redis/go-redis/v9"
)

//go:embed template/*.html
var templateFS embed.FS

const otpVerifyRateLimitCount = 5

type OTPService struct {
	client         *ent.Client
	cfg            *config.AppConfig
	validator      *validator.Validate
	emailAdapter   otpEmailSender
	captchaAdapter otpCaptchaVerifier
	redisAdapter   otpStore
	rateLimitRepo  otpRateLimiter
}

type otpEmailSender = adapter.EmailSender
type otpCaptchaVerifier = adapter.CaptchaVerifier
type otpStore = adapter.RedisStore
type otpRateLimiter = repository.RateLimiter

func NewOTPService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, emailAdapter otpEmailSender, captchaAdapter otpCaptchaVerifier, redisAdapter otpStore, rateLimitRepo otpRateLimiter) *OTPService {
	return &OTPService{
		client:         client,
		cfg:            cfg,
		validator:      validator,
		emailAdapter:   emailAdapter,
		captchaAdapter: captchaAdapter,
		redisAdapter:   redisAdapter,
		rateLimitRepo:  rateLimitRepo,
	}
}

func (s *OTPService) SendOTP(ctx context.Context, req model.SendOTPRequest) error {
	req.Email = helper.NormalizeEmail(req.Email)

	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return helper.NewBadRequestError("")
	}

	if err := s.captchaAdapter.Verify(req.CaptchaToken, ""); err != nil {
		slog.Warn("Captcha verification failed", "error", err)
		return helper.NewBadRequestError("")
	}

	rateLimitScope := s.clientRateLimitScope(ctx)
	rateLimitKey := fmt.Sprintf("ratelimit:otp:%s:%s:%s", req.Mode, req.Email, rateLimitScope)

	limit := 1
	window := time.Duration(s.cfg.OTPRateLimitSeconds) * time.Second
	if window == 0 {
		window = 60 * time.Second
	}

	allowed, ttl, err := s.rateLimitRepo.Allow(ctx, rateLimitKey, limit, window)
	if err != nil {
		slog.Error("Failed to check rate limit", "error", err)

		return helper.NewInternalServerError("")
	}

	if !allowed {
		seconds := int(math.Ceil(ttl.Seconds()))
		return helper.NewTooManyRequestsError(fmt.Sprintf("Please try again in %d seconds", seconds))
	}

	userExists, err := s.client.User.Query().
		Where(user.Email(req.Email)).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check user existence", "error", err)
		return helper.NewInternalServerError("")
	}

	mode := constant.OTPMode(req.Mode)

	shouldSend := true

	if (mode == constant.ModeRegister || mode == constant.ModeChangeEmail) && userExists {
		shouldSend = false
		slog.Info("OTP request suppressed: Email already registered", "email", req.Email, "mode", mode)
	}

	if mode == constant.ModeReset && !userExists {
		shouldSend = false
		slog.Info("OTP request suppressed: Email not found for reset", "email", req.Email)
	}

	if !shouldSend {
		return nil
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		slog.Error("Failed to generate random number", "error", err)
		return helper.NewInternalServerError("")
	}

	code := fmt.Sprintf("%06d", n.Int64())
	hashedCode := helper.HashOTP(code, s.cfg.OTPSecret)

	key := fmt.Sprintf("otp:%s:%s", req.Mode, req.Email)
	otpTTL := time.Duration(s.cfg.OTPExp) * time.Second

	err = s.redisAdapter.Set(ctx, key, hashedCode, otpTTL)
	if err != nil {
		slog.Error("Failed to save OTP to Redis", "error", err)
		return helper.NewInternalServerError("")
	}

	sendEmail := func() {
		templateData := struct {
			Code    string
			Expired int
			Year    int
		}{
			Code:    code,
			Expired: s.cfg.OTPExp / 60,
			Year:    time.Now().UTC().Year(),
		}

		emailBody, err := helper.GenerateEmailBody(templateFS, "template/verify_email.html", templateData)
		if err != nil {
			slog.Error("Failed to generate email body", "error", err)
			return
		}

		err = s.emailAdapter.Send([]string{req.Email}, "Your OTP Code", emailBody)
		if err != nil {
			slog.Error("Failed to send OTP email", "error", err)

			_ = s.redisAdapter.Del(context.Background(), key)
		}
	}

	if s.cfg.SMTPAsync {
		go sendEmail()
	} else {
		sendEmail()
	}

	return nil
}

func (s *OTPService) VerifyOTP(ctx context.Context, email, code, mode string) error {
	email = helper.NormalizeEmail(email)

	rateLimitScope := s.clientRateLimitScope(ctx)
	verifyRateLimitKey := fmt.Sprintf("ratelimit:otp_verify:%s:%s:%s", mode, email, rateLimitScope)
	verifyWindow := time.Duration(s.cfg.OTPRateLimitSeconds) * time.Second
	if verifyWindow <= 0 {
		verifyWindow = 60 * time.Second
	}

	allowed, ttl, err := s.rateLimitRepo.Allow(ctx, verifyRateLimitKey, otpVerifyRateLimitCount, verifyWindow)
	if err != nil {
		slog.Error("Failed to check OTP verify rate limit", "error", err, "email", email, "mode", mode)
		return helper.NewServiceUnavailableError("OTP service unavailable")
	}
	if !allowed {
		seconds := int(math.Ceil(ttl.Seconds()))
		return helper.NewTooManyRequestsError(fmt.Sprintf("Please try again in %d seconds", seconds))
	}

	key := fmt.Sprintf("otp:%s:%s", mode, email)

	hashedCode, err := s.redisAdapter.Get(ctx, key)
	if err != nil {
		if err == redis.Nil {
			return helper.NewBadRequestError("Invalid or expired OTP")
		}
		slog.Error("Failed to get OTP from Redis", "error", err, "email", email, "mode", mode)
		return helper.NewServiceUnavailableError("OTP service unavailable")
	}

	if hashedCode != helper.HashOTP(code, s.cfg.OTPSecret) {
		return helper.NewBadRequestError("Invalid or expired OTP")
	}

	if err := s.redisAdapter.Del(ctx, key); err != nil {
		slog.Error("Failed to delete used OTP key", "error", err, "email", email, "mode", mode)
		return helper.NewServiceUnavailableError("OTP service unavailable")
	}

	return nil
}

func (s *OTPService) clientRateLimitScope(ctx context.Context) string {
	fingerprint := helper.ClientFingerprintFromContext(ctx)
	if fingerprint == "" {
		return "global"
	}

	return helper.HashOTP(fingerprint, s.cfg.OTPSecret)
}
