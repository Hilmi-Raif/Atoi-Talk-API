package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/pushdevice"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Service struct {
	client *ent.Client
	cfg    *config.AppConfig
}

func NewService(client *ent.Client, cfg *config.AppConfig) *Service {
	return &Service{client: client, cfg: cfg}
}
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Upsert(ctx context.Context, userID uuid.UUID, session string, installation uuid.UUID, req model.PushDeviceRequest) error {
	if !s.cfg.PushEnabled {
		return helper.NewServiceUnavailableError("Push notifications are disabled")
	}
	if installation == uuid.Nil || req.Token == "" || len(req.Token) > 4096 || strings.TrimSpace(req.Token) != req.Token || len(req.DeviceSecret) < 32 || len(req.DeviceSecret) > 256 || (req.Platform != "android" && req.Platform != "ios" && req.Platform != "web") {
		return helper.NewBadRequestError("Invalid device registration")
	}
	claims := &helper.JWTClaims{}
	_, err := jwt.ParseWithClaims(session, claims, func(token *jwt.Token) (interface{}, error) { return []byte(s.cfg.JWTSecret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || claims.UserID != userID || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return helper.NewUnauthorizedError("")
	}
	issued := claims.IssuedAtMillis
	if issued <= 0 {
		issued = claims.IssuedAt.UnixMilli()
	}
	sessionHash, secretHash := Fingerprint(session), Fingerprint(req.DeviceSecret)
	existing, err := s.client.PushDevice.Query().Where(pushdevice.InstallationID(installation)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return helper.NewInternalServerError("")
	}
	if existing != nil {
		if existing.SecretHash != secretHash {
			return helper.NewForbiddenError("Installation proof does not match")
		}
		binding := existing.BindingID
		if existing.UserID != userID || existing.Token != req.Token || existing.SessionHash != sessionHash || existing.DisabledAt != nil {
			binding = uuid.New()
		}
		n, updateErr := s.client.PushDevice.Update().Where(pushdevice.ID(existing.ID), pushdevice.BindingID(existing.BindingID), pushdevice.SecretHash(secretHash)).SetUserID(userID).SetToken(req.Token).SetPlatform(pushdevice.Platform(req.Platform)).SetSessionHash(sessionHash).SetIssuedAt(issued).SetExpiresAt(claims.ExpiresAt.Time).SetBindingID(binding).ClearDisabledAt().Save(ctx)
		if ent.IsConstraintError(updateErr) || n == 0 && updateErr == nil {
			return helper.NewConflictError("Registration changed; retry")
		}
		if updateErr != nil {
			return helper.NewInternalServerError("")
		}
		return nil
	}
	_, err = s.client.PushDevice.Create().SetInstallationID(installation).SetUserID(userID).SetToken(req.Token).SetPlatform(pushdevice.Platform(req.Platform)).SetSessionHash(sessionHash).SetSecretHash(secretHash).SetIssuedAt(issued).SetExpiresAt(claims.ExpiresAt.Time).SetBindingID(uuid.New()).Save(ctx)
	if ent.IsConstraintError(err) {
		return helper.NewConflictError("Device or token already registered")
	}
	if err != nil {
		return helper.NewInternalServerError("")
	}
	return nil
}

func (s *Service) Unregister(ctx context.Context, userID uuid.UUID, session string, installation uuid.UUID) error {
	n, err := s.client.PushDevice.Update().Where(pushdevice.InstallationID(installation), pushdevice.UserID(userID), pushdevice.SessionHash(Fingerprint(session))).SetDisabledAt(time.Now().UTC()).SetBindingID(uuid.New()).Save(ctx)
	if err != nil {
		return helper.NewInternalServerError("")
	}
	if n == 0 {
		return helper.NewNotFoundError("Device not found")
	}
	return nil
}
