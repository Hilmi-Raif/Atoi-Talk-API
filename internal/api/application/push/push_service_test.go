package push

import (
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRegistrationUpsertRotationAndAccountSwitch(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, dialect.SQLite, "file:push-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	service := NewService(client, &config.AppConfig{JWTSecret: "test", PushEnabled: true})
	first, second := uuid.New(), uuid.New()
	jwt1, err := helper.GenerateJWT("test", 3600, first)
	require.NoError(t, err)
	jwt2, err := helper.GenerateJWT("test", 3600, second)
	require.NoError(t, err)
	installation := uuid.New()
	request := model.PushDeviceRequest{Token: "fcm-token", Platform: "android", DeviceSecret: "01234567890123456789012345678901"}
	require.NoError(t, service.Upsert(ctx, first, jwt1, installation, request))
	device := client.PushDevice.Query().OnlyX(ctx)
	require.NoError(t, service.Upsert(ctx, first, jwt1, installation, request))
	require.Equal(t, device.BindingID, client.PushDevice.Query().OnlyX(ctx).BindingID)
	forged := request
	forged.DeviceSecret = "99999999999999999999999999999999"
	require.Error(t, service.Upsert(ctx, second, jwt2, installation, forged))
	require.Equal(t, first, client.PushDevice.Query().OnlyX(ctx).UserID)
	request.Token = "refreshed-token"
	require.NoError(t, service.Upsert(ctx, second, jwt2, installation, request))
	switched := client.PushDevice.Query().OnlyX(ctx)
	require.NotEqual(t, device.BindingID, switched.BindingID)
	require.Equal(t, second, switched.UserID)
	require.Error(t, service.Unregister(ctx, first, jwt1, installation))
	require.NoError(t, service.Unregister(ctx, second, jwt2, installation))
	require.NotNil(t, client.PushDevice.Query().OnlyX(ctx).DisabledAt)
	require.NoError(t, service.Upsert(ctx, second, jwt2, installation, request))
	require.Nil(t, client.PushDevice.Query().OnlyX(ctx).DisabledAt)
	require.Equal(t, 1, client.PushDevice.Query().CountX(ctx))
}

func TestRegistrationRejectsForeignTokenAndExpiredSession(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, dialect.SQLite, "file:push-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	service := NewService(client, &config.AppConfig{JWTSecret: "test", PushEnabled: true})
	owner := uuid.New()
	token, err := helper.GenerateJWT("test", 3600, owner)
	require.NoError(t, err)
	req := model.PushDeviceRequest{Token: "same-token", Platform: "web", DeviceSecret: "01234567890123456789012345678901"}
	require.NoError(t, service.Upsert(ctx, owner, token, uuid.New(), req))
	require.Error(t, service.Upsert(ctx, owner, token, uuid.New(), req))
	expired, err := helper.GenerateJWT("test", -int(time.Hour.Seconds()), owner)
	require.NoError(t, err)
	require.Error(t, service.Upsert(ctx, owner, expired, uuid.New(), req))
	require.Equal(t, 1, client.PushDevice.Query().CountX(ctx))
}

func TestRegistrationValidatesInputAndFeatureFlag(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, dialect.SQLite, "file:push-validation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	cfg := &config.AppConfig{JWTSecret: "test"}
	s := NewService(client, cfg)
	owner := uuid.New()
	token, err := helper.GenerateJWT("test", 3600, owner)
	require.NoError(t, err)
	req := model.PushDeviceRequest{Token: "token", Platform: "web", DeviceSecret: "01234567890123456789012345678901"}
	require.Error(t, s.Upsert(ctx, owner, token, uuid.New(), req))
	cfg.PushEnabled = true
	require.Error(t, s.Upsert(ctx, owner, token, uuid.Nil, req))
	invalid := req
	invalid.Platform = "unknown"
	require.Error(t, s.Upsert(ctx, owner, token, uuid.New(), invalid))
	require.Error(t, s.Upsert(ctx, owner, "invalid-token", uuid.New(), req))
	require.Error(t, s.Unregister(ctx, owner, token, uuid.New()))
	require.Zero(t, client.PushDevice.Query().CountX(ctx))
}
