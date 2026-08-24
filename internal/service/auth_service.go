package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/media"
	_ "AtoiTalkAPI/ent/runtime"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/useridentity"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/constant"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/websocket"
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleOAuth2 "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

const (
	googleOAuthStateKeyPrefix = "oauth:google:state:"
	googleOAuthStateTTL       = 10 * time.Minute
)

type googleOAuthStatePayload struct {
	CodeVerifier    string `json:"code_verifier"`
	FingerprintHash string `json:"fingerprint_hash,omitempty"`
}

type AuthService struct {
	client         *ent.Client
	cfg            *config.AppConfig
	validator      *validator.Validate
	storageAdapter authStorage
	captchaAdapter authCaptcha
	redisAdapter   authRedis
	otpService     authOTP
	sessionStore   authSessionStore
	wsHub          websocket.Publisher
}

type authOTP interface {
	VerifyOTP(ctx context.Context, email string, code string, mode string) error
}

type authStorage = adapter.AuthStorage
type authCaptcha = adapter.CaptchaVerifier
type authRedis = adapter.RedisOAuthStore
type authSessionStore = repository.SessionStore

func NewAuthService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, storageAdapter authStorage, captchaAdapter authCaptcha, redisAdapter authRedis, otpService authOTP, sessionStore authSessionStore, wsHub websocket.Publisher) *AuthService {
	return &AuthService{
		client:         client,
		cfg:            cfg,
		validator:      validator,
		storageAdapter: storageAdapter,
		captchaAdapter: captchaAdapter,
		redisAdapter:   redisAdapter,
		otpService:     otpService,
		sessionStore:   sessionStore,
		wsHub:          wsHub,
	}
}

func (s *AuthService) googleOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.GoogleClientID,
		ClientSecret: s.cfg.GoogleClientSecret,
		RedirectURL:  s.cfg.GoogleRedirectURL,
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}
}

func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *AuthService) BeginGoogleAuth(ctx context.Context) (*model.GoogleAuthInitResponse, error) {
	state, err := helper.GenerateRandomString(48)
	if err != nil {
		slog.Error("Failed to generate Google OAuth state", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	codeVerifier, err := helper.GenerateRandomString(64)
	if err != nil {
		slog.Error("Failed to generate Google OAuth PKCE verifier", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	payload := googleOAuthStatePayload{CodeVerifier: codeVerifier}
	if fingerprint := helper.ClientFingerprintFromContext(ctx); fingerprint != "" {
		payload.FingerprintHash = helper.HashOTP(fingerprint, s.cfg.OTPSecret)
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal Google OAuth state payload", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	stateKey := fmt.Sprintf("%s%s", googleOAuthStateKeyPrefix, state)
	if err := s.redisAdapter.Set(ctx, stateKey, payloadBytes, googleOAuthStateTTL); err != nil {
		slog.Error("Failed to persist Google OAuth state", "error", err)
		return nil, helper.NewServiceUnavailableError("Session service unavailable")
	}

	conf := s.googleOAuthConfig()
	authURL := conf.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", codeChallengeS256(codeVerifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	return &model.GoogleAuthInitResponse{
		AuthURL:          authURL,
		State:            state,
		ExpiresInSeconds: int(googleOAuthStateTTL.Seconds()),
	}, nil
}

func (s *AuthService) Logout(ctx context.Context, tokenString string) error {
	parsedToken, err := jwt.ParseWithClaims(tokenString, &helper.JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil {
		slog.Warn("Failed to parse JWT token on logout, using default TTL", "error", err)
	}

	var ttl time.Duration
	var userID uuid.UUID

	if err == nil && parsedToken != nil {
		if claims, ok := parsedToken.Claims.(*helper.JWTClaims); ok {
			userID = claims.UserID
			if claims.ExpiresAt != nil {
				ttl = time.Until(claims.ExpiresAt.Time)
			}
		}
	}

	if ttl <= 0 {
		ttl = time.Duration(s.cfg.JWTExp) * time.Second
	}

	err = s.sessionStore.BlacklistToken(ctx, tokenString, ttl)
	if err != nil {
		slog.Error("Failed to blacklist token on logout", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil && userID != uuid.Nil {
		go s.wsHub.DisconnectUser(userID)
	}

	return nil
}

func (s *AuthService) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	return s.sessionStore.RevokeAllSessions(ctx, userID)
}

func (s *AuthService) VerifyUser(ctx context.Context, tokenString string) (*model.UserDTO, error) {
	token, err := jwt.ParseWithClaims(tokenString, &helper.JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})

	if err != nil {
		slog.Warn("Failed to parse JWT token", "error", err)
		return nil, helper.NewUnauthorizedError("")
	}

	claims, ok := token.Claims.(*helper.JWTClaims)
	if !ok || !token.Valid {
		return nil, helper.NewUnauthorizedError("")
	}

	if claims.IssuedAt == nil {
		return nil, helper.NewUnauthorizedError("")
	}

	tokenIssuedAt := claims.IssuedAt.UnixMilli()
	if claims.IssuedAtMillis > 0 {
		tokenIssuedAt = claims.IssuedAtMillis
	}

	isRevoked, err := s.sessionStore.IsUserRevoked(ctx, claims.UserID, tokenIssuedAt)
	if err != nil {
		slog.Error("Failed to check user revoked session", "error", err, "userID", claims.UserID)
		return nil, helper.NewServiceUnavailableError("Session service unavailable")
	}

	if isRevoked {
		return nil, helper.NewUnauthorizedError("")
	}

	u, err := s.client.User.Query().
		Where(
			user.ID(claims.UserID),
			user.DeletedAtIsNil(),
		).
		Select(user.FieldID, user.FieldRole, user.FieldIsBanned, user.FieldBannedUntil).
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewUnauthorizedError("")
		}
		slog.Error("Failed to check user existence", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if u.IsBanned {
		if u.BannedUntil != nil {
			if time.Now().Before(*u.BannedUntil) {
				return nil, helper.NewForbiddenError("Account is temporarily suspended")
			}

			_, err := s.client.User.UpdateOne(u).
				SetIsBanned(false).
				ClearBannedUntil().
				ClearBanReason().
				Save(ctx)
			if err != nil {
				slog.Error("Failed to lift expired ban in VerifyUser", "error", err)
			}
		} else {
			return nil, helper.NewForbiddenError("Account is permanently banned")
		}
	}

	return &model.UserDTO{
		ID:   claims.UserID,
		Role: string(u.Role),
	}, nil
}

func (s *AuthService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	req.Email = helper.NormalizeEmail(req.Email)

	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	if err := s.captchaAdapter.Verify(req.CaptchaToken, ""); err != nil {
		slog.Warn("Captcha verification failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	u, err := s.client.User.Query().
		Where(
			user.Email(req.Email),
			user.DeletedAtIsNil(),
		).
		WithAvatar().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewUnauthorizedError("")
		}
		slog.Error("Failed to query user", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if u.PasswordHash == nil || !helper.CheckPasswordHash(req.Password, *u.PasswordHash) {
		return nil, helper.NewUnauthorizedError("")
	}

	if u.IsBanned {
		if u.BannedUntil != nil {
			if time.Now().Before(*u.BannedUntil) {

				return nil, helper.NewForbiddenError(fmt.Sprintf("Account suspended until %s", u.BannedUntil.UTC().Format(time.RFC3339)))
			}

			_, err := s.client.User.UpdateOne(u).
				SetIsBanned(false).
				ClearBannedUntil().
				ClearBanReason().
				Save(ctx)
			if err != nil {
				slog.Error("Failed to lift expired ban", "error", err)
			}
		} else {
			return nil, helper.NewForbiddenError("Account is permanently banned")
		}
	}

	token, err := helper.GenerateJWT(s.cfg.JWTSecret, s.cfg.JWTExp, u.ID)
	if err != nil {
		slog.Error("Failed to generate JWT token", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	avatarURL := ""
	if u.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
	}

	username := ""
	if u.Username != nil {
		username = *u.Username
	}

	fullName := ""
	if u.FullName != nil {
		fullName = *u.FullName
	}

	return &model.AuthResponse{
		Token: token,
		User: model.UserDTO{
			ID:       u.ID,
			Email:    *u.Email,
			Username: username,
			FullName: fullName,
			Avatar:   avatarURL,
			Role:     string(u.Role),
		},
	}, nil
}

func (s *AuthService) GoogleExchange(ctx context.Context, req model.GoogleLoginRequest) (*model.AuthResponse, error) {
	if err := s.validator.Struct(&req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	stateKey := fmt.Sprintf("%s%s", googleOAuthStateKeyPrefix, req.State)
	payloadRaw, err := s.redisAdapter.GetDel(ctx, stateKey)
	if err != nil {
		if err == redis.Nil {
			return nil, helper.NewUnauthorizedError("Invalid or expired Google auth state")
		}
		slog.Error("Failed to load Google OAuth state", "error", err)
		return nil, helper.NewServiceUnavailableError("Session service unavailable")
	}

	var statePayload googleOAuthStatePayload
	if err := json.Unmarshal([]byte(payloadRaw), &statePayload); err != nil || statePayload.CodeVerifier == "" {
		slog.Warn("Invalid Google OAuth state payload", "error", err)
		return nil, helper.NewUnauthorizedError("Invalid or expired Google auth state")
	}

	if statePayload.FingerprintHash != "" {
		currentFingerprint := helper.ClientFingerprintFromContext(ctx)
		if currentFingerprint == "" || helper.HashOTP(currentFingerprint, s.cfg.OTPSecret) != statePayload.FingerprintHash {
			return nil, helper.NewUnauthorizedError("Invalid Google auth state")
		}
	}

	conf := s.googleOAuthConfig()

	oauthToken, err := conf.Exchange(ctx, req.Code, oauth2.SetAuthURLParam("code_verifier", statePayload.CodeVerifier))
	if err != nil {
		slog.Error("Failed to exchange authorization code", "error", err)
		return nil, helper.NewUnauthorizedError("Invalid authorization code")
	}

	oauth2Service, err := googleOAuth2.NewService(ctx, option.WithTokenSource(conf.TokenSource(ctx, oauthToken)), option.WithHTTPClient(oauth2.NewClient(ctx, conf.TokenSource(ctx, oauthToken))))
	if err != nil {
		slog.Error("Failed to create oauth2 service", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	userInfo, err := oauth2Service.Userinfo.Get().Do()
	if err != nil {
		slog.Error("Failed to get user info from google", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	email := userInfo.Email
	if email == "" {
		slog.Warn("Email not found in google user info")
		return nil, helper.NewBadRequestError("Email not found")
	}

	if userInfo.VerifiedEmail == nil || !*userInfo.VerifiedEmail {
		slog.Warn("Email from Google is not verified", "email", email)
		return nil, helper.NewBadRequestError("Email from Google is not verified.")
	}

	email = helper.NormalizeEmail(email)

	name := userInfo.Name
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	name = strings.TrimSpace(name)

	picture := userInfo.Picture
	sub := userInfo.Id

	if sub == "" {
		slog.Warn("Subject ID (Google ID) not found")
		return nil, helper.NewBadRequestError("Invalid Google ID")
	}

	u, err := s.client.User.Query().
		Where(
			user.Email(email),
			user.DeletedAtIsNil(),
		).
		WithAvatar().
		Only(ctx)

	if err != nil && !ent.IsNotFound(err) {
		slog.Error("Failed to query user", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if u != nil && u.IsBanned {
		if u.BannedUntil != nil {
			if time.Now().Before(*u.BannedUntil) {

				return nil, helper.NewForbiddenError(fmt.Sprintf("Account suspended until %s", u.BannedUntil.UTC().Format(time.RFC3339)))
			}

			_, err := s.client.User.UpdateOne(u).
				SetIsBanned(false).
				ClearBannedUntil().
				ClearBanReason().
				Save(ctx)
			if err != nil {
				slog.Error("Failed to lift expired ban", "error", err)
			}
		} else {
			return nil, helper.NewForbiddenError("Account is permanently banned")
		}
	}

	var avatarFileName string
	var fileSize int64
	var mimeType string

	var fileData []byte
	var fileUploadPath string
	var fileContentType string
	var mediaID uuid.UUID
	avatarUploadSucceeded := true

	if u == nil {

		if picture != "" {
			data, contentType, err := s.storageAdapter.Download(picture)
			if err != nil {
				slog.Error("Failed to download profile picture", "error", err)
			} else {
				fileName := helper.GenerateUniqueFileName(picture)

				filePath := fileName
				fileSize = int64(len(data))
				mimeType = contentType

				if mimeType == "" {
					mimeType = "image/jpeg"
				}

				fileData = data
				fileUploadPath = filePath
				fileContentType = mimeType
				avatarFileName = fileName
			}
		}

		tx, err := s.client.Tx(ctx)
		if err != nil {
			slog.Error("Failed to start transaction", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		defer func() {
			_ = tx.Rollback()
			if v := recover(); v != nil {
				panic(v)
			}
		}()

		baseUsername := strings.Split(email, "@")[0]
		baseUsername = helper.NormalizeUsername(baseUsername)
		if len(baseUsername) > 40 {
			baseUsername = baseUsername[:40]
		}
		if len(baseUsername) < 3 {
			baseUsername = "user" + baseUsername
		}

		var finalUsername string
		for i := 0; i < 3; i++ {
			randNum, randErr := crand.Int(crand.Reader, big.NewInt(10000))
			if randErr != nil {
				slog.Error("Failed to generate username suffix", "error", randErr)
				return nil, helper.NewInternalServerError("")
			}
			candidate := fmt.Sprintf("%s%04d", baseUsername, randNum.Int64())

			exists, existsErr := tx.User.Query().Where(user.UsernameEQ(candidate)).Exist(ctx)
			if existsErr != nil {
				slog.Error("Failed to check generated username availability", "error", existsErr)
				return nil, helper.NewInternalServerError("")
			}
			if !exists {
				finalUsername = candidate
				break
			}
		}

		if finalUsername == "" {
			return nil, helper.NewConflictError("Failed to generate unique username")
		}

		u, err = tx.User.Create().
			SetEmail(email).
			SetUsername(finalUsername).
			SetFullName(name).
			Save(ctx)
		if err != nil {
			slog.Error("Failed to create user", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if fileData != nil {
			media, err := tx.Media.Create().
				SetFileName(avatarFileName).
				SetOriginalName(filepath.Base(picture)).
				SetFileSize(fileSize).
				SetMimeType(mimeType).
				SetCategory(media.CategoryUserAvatar).
				SetUploader(u).
				Save(ctx)

			if err != nil {
				slog.Error("Failed to create media record for google avatar", "error", err)
				fileData = nil
				avatarFileName = ""
			} else {
				err = tx.User.UpdateOne(u).SetAvatar(media).Exec(ctx)
				if err != nil {
					slog.Error("Failed to link avatar to user", "error", err)
					avatarFileName = ""
				} else {
					mediaID = media.ID
				}
			}
		}

		_, err = tx.UserIdentity.Create().
			SetUser(u).
			SetProvider(useridentity.ProviderGoogle).
			SetProviderID(sub).
			SetProviderEmail(email).
			Save(ctx)

		if err != nil {
			slog.Error("Failed to create user identity", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if err := tx.Commit(); err != nil {
			slog.Error("Failed to commit transaction", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if fileData != nil {

			err = s.storageAdapter.StoreFromReader(bytes.NewReader(fileData), fileContentType, fileUploadPath, true)
			if err != nil {
				avatarUploadSucceeded = false
				slog.Error("Failed to store profile picture after db commit", "error", err)

				if mediaID != uuid.Nil {
					if delErr := s.client.Media.DeleteOneID(mediaID).Exec(context.Background()); delErr != nil {
						slog.Error("Failed to delete orphan media record after file upload failure", "error", delErr, "mediaID", mediaID)
					}
				}
				avatarFileName = ""
			}
		}

	} else {

		exists, err := s.client.UserIdentity.Query().
			Where(
				useridentity.UserID(u.ID),
				useridentity.ProviderEQ(useridentity.ProviderGoogle),
				useridentity.ProviderID(sub),
			).
			Exist(ctx)

		if err != nil {
			slog.Error("Failed to check user identity", "error", err)
		} else if !exists {
			_, err = s.client.UserIdentity.Create().
				SetUserID(u.ID).
				SetProvider(useridentity.ProviderGoogle).
				SetProviderID(sub).
				SetProviderEmail(email).
				Save(ctx)

			if err != nil {
				slog.Error("Failed to link google identity to existing user", "error", err)
			}
		}

		if u.Edges.Avatar != nil {
			avatarFileName = u.Edges.Avatar.FileName
		}
	}

	jwtToken, err := helper.GenerateJWT(s.cfg.JWTSecret, s.cfg.JWTExp, u.ID)
	if err != nil {
		slog.Error("Failed to generate JWT token", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	avatarURL := ""
	if avatarUploadSucceeded && avatarFileName != "" {
		avatarURL = s.storageAdapter.GetPublicURL(avatarFileName)
	}

	username := ""
	if u.Username != nil {
		username = *u.Username
	}

	fullName := ""
	if u.FullName != nil {
		fullName = *u.FullName
	}

	return &model.AuthResponse{
		Token: jwtToken,
		User: model.UserDTO{
			ID:       u.ID,
			Email:    *u.Email,
			Username: username,
			FullName: fullName,
			Avatar:   avatarURL,
			Role:     string(u.Role),
		},
	}, nil
}

func (s *AuthService) Register(ctx context.Context, req model.RegisterUserRequest) (*model.AuthResponse, error) {
	req.Email = helper.NormalizeEmail(req.Email)
	req.Username = helper.NormalizeUsername(req.Username)
	req.FullName = strings.TrimSpace(req.FullName)

	if err := s.validator.Struct(&req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	if err := s.captchaAdapter.Verify(req.CaptchaToken, ""); err != nil {
		slog.Warn("Captcha verification failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	if err := s.otpService.VerifyOTP(ctx, req.Email, req.Code, string(constant.ModeRegister)); err != nil {
		return nil, err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	hashedPassword, err := helper.HashPassword(req.Password)
	if err != nil {
		slog.Error("Failed to hash password", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	newUser, err := tx.User.Create().
		SetEmail(req.Email).
		SetUsername(req.Username).
		SetFullName(req.FullName).
		SetPasswordHash(hashedPassword).
		Save(ctx)

	if err != nil {
		if ent.IsConstraintError(err) {
			_ = tx.Rollback()

			emailExists, existsErr := s.client.User.Query().
				Where(user.Email(req.Email), user.DeletedAtIsNil()).
				Exist(ctx)
			if existsErr != nil {
				slog.Error("Failed to check email existence after register constraint error", "error", existsErr)
				return nil, helper.NewInternalServerError("")
			}
			if emailExists {
				return nil, helper.NewConflictError("Email already registered")
			}

			usernameExists, usernameExistsErr := s.client.User.Query().
				Where(user.UsernameEQ(req.Username), user.DeletedAtIsNil()).
				Exist(ctx)
			if usernameExistsErr != nil {
				slog.Error("Failed to check username existence after register constraint error", "error", usernameExistsErr)
				return nil, helper.NewInternalServerError("")
			}
			if usernameExists {
				return nil, helper.NewConflictError("Username already taken")
			}

			return nil, helper.NewConflictError("Email or Username already taken")
		}
		slog.Error("Failed to create user", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	token, err := helper.GenerateJWT(s.cfg.JWTSecret, s.cfg.JWTExp, newUser.ID)
	if err != nil {
		slog.Error("Failed to generate JWT token", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	fullName := ""
	if newUser.FullName != nil {
		fullName = *newUser.FullName
	}

	return &model.AuthResponse{
		Token: token,
		User: model.UserDTO{
			ID:       newUser.ID,
			Email:    *newUser.Email,
			Username: *newUser.Username,
			FullName: fullName,
			Role:     string(newUser.Role),
		},
	}, nil
}

func (s *AuthService) ResetPassword(ctx context.Context, req model.ResetPasswordRequest) error {
	req.Email = helper.NormalizeEmail(req.Email)

	if err := s.validator.Struct(&req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return helper.NewBadRequestError("")
	}

	if err := s.captchaAdapter.Verify(req.CaptchaToken, ""); err != nil {
		slog.Warn("Captcha verification failed", "error", err)
		return helper.NewBadRequestError("")
	}

	if err := s.otpService.VerifyOTP(ctx, req.Email, req.Code, string(constant.ModeReset)); err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	u, err := tx.User.Query().
		Where(
			user.Email(req.Email),
			user.DeletedAtIsNil(),
		).
		Select(user.FieldID).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query user", "error", err)
		return helper.NewInternalServerError("")
	}

	hashedPassword, err := helper.HashPassword(req.Password)
	if err != nil {
		slog.Error("Failed to hash password", "error", err)
		return helper.NewInternalServerError("")
	}

	err = tx.User.UpdateOne(u).
		SetPasswordHash(hashedPassword).
		Exec(ctx)
	if err != nil {
		slog.Error("Failed to update password", "error", err)
		return helper.NewInternalServerError("")
	}

	revokeExpected, revokeSnapshot, err := helper.RevokeSessionsForTransaction(ctx, s.sessionStore, u.ID)
	if err != nil {
		slog.Error("Failed to revoke sessions after password reset", "error", err, "userID", u.ID)
		return helper.NewServiceUnavailableError("Session service unavailable")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		helper.RollbackSessionRevokeIfNeeded(s.sessionStore, u.ID, revokeExpected, revokeSnapshot)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go s.wsHub.DisconnectUser(u.ID)
	}

	return nil
}
