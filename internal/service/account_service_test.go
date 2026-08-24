package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/useridentity"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/constant"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newAccountServiceTest(t *testing.T) (*AccountService, *ent.Client, *repositorymocks.MockSessionStore, *servicemocks.MockauthOTP) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:account-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	sessionStore := repositorymocks.NewMockSessionStore(t)
	otpService := servicemocks.NewMockauthOTP(t)
	service := NewAccountService(
		client,
		&config.AppConfig{},
		config.NewValidator(),
		nil,
		otpService,
		sessionStore,
	)
	return service, client, sessionStore, otpService
}

func expectSessionRevoke(t *testing.T, sessionStore *repositorymocks.MockSessionStore, userID uuid.UUID) {
	t.Helper()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, userID).
		Return(helper.SessionRevokeSnapshot{}, nil)
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, userID, mock.AnythingOfType("int64")).
		Return("revoke-marker", nil)
}

func TestAccountServiceChangePasswordUpdatesPasswordAndRevokesSessions(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("OldPass1!"),
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.NotNil(t, updated.PasswordHash)
	require.True(t, helper.CheckPasswordHash("NewPass2@", *updated.PasswordHash))
}

func TestAccountServiceChangePasswordDisconnectsUser(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	hub := websocketmocks.NewMockPublisher(t)
	service.wsHub = hub
	disconnected := make(chan uuid.UUID, 1)
	hub.EXPECT().DisconnectUser(user.ID).Run(func(id uuid.UUID) { disconnected <- id }).Once()

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("OldPass1!"),
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})
	require.NoError(t, err)
	select {
	case id := <-disconnected:
		require.Equal(t, user.ID, id)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect")
	}
}

func TestAccountServiceChangePasswordRejectsWrongOldPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("WrongPass1!"),
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "Invalid old password")
}

func TestAccountServiceChangePasswordRequiresOldPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.Error(t, err)
}

func TestAccountServiceChangePasswordAllowsAccountWithoutPassword(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	user := client.User.Create().SetUsername("account-user").SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	err := service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.NoError(t, err)
}

func TestAccountServiceChangePasswordFailsSessionRevokeAndRollsBack(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, user.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("session unavailable"))

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("OldPass1!"),
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.Error(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.True(t, helper.CheckPasswordHash("OldPass1!", *updated.PasswordHash))
}

func TestAccountServiceChangePasswordRejectsSessionRevokeFailure(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, user.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("session unavailable"))

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("OldPass1!"),
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.Error(t, err)
}

func TestAccountServiceChangePasswordRejectsInvalidRequest(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	err := service.ChangePassword(context.Background(), uuid.New(), model.ChangePasswordRequest{NewPassword: "short"})

	require.Error(t, err)
}

func TestAccountServiceChangePasswordReturnsNotFoundForUnknownUser(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	err := service.ChangePassword(context.Background(), uuid.New(), model.ChangePasswordRequest{
		NewPassword:     "NewPass2@",
		ConfirmPassword: "NewPass2@",
	})

	require.Error(t, err)
}

func TestAccountServiceChangeEmailUpdatesEmailAndRevokesSessions(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil)
	expectSessionRevoke(t, sessionStore, user.ID)

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: " NEW@example.com ", Code: "123456"})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.NotNil(t, updated.Email)
	require.Equal(t, "new@example.com", *updated.Email)
}

func TestAccountServiceChangeEmailRejectsExistingEmail(t *testing.T) {
	service, client, _, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)
	client.User.Create().SetUsername("other-user").SetEmail("new@example.com").SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil)

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "Email already registered")
}

func TestAccountServiceChangeEmailRejectsAccountWithoutPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SaveX(ctx)

	err := service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})

	require.Error(t, err)
}

func TestAccountServiceChangeEmailRejectsSameEmail(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: " OLD@example.com ", Code: "123456"})

	require.Error(t, err)
}

func TestAccountServiceChangeEmailReturnsNotFoundForUnknownUser(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	err := service.ChangeEmail(context.Background(), uuid.New(), model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})

	require.Error(t, err)
}

func TestAccountServiceChangeEmailReturnsOTPError(t *testing.T) {
	service, client, _, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeChangeEmail)).Return(errors.New("invalid otp"))

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})

	require.Error(t, err)
}

func TestAccountServiceChangeEmailRejectsInvalidRequestBeforeOTP(t *testing.T) {
	service, client, _, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	err := service.ChangeEmail(context.Background(), uuid.New(), model.ChangeEmailRequest{Email: "invalid", Code: "short"})

	require.Error(t, err)
	otpService.AssertNotCalled(t, "VerifyOTP", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAccountServiceChangeEmailReturnsSessionServiceError(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil)
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, user.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("session unavailable"))

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})

	require.Error(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.Equal(t, "old@example.com", *updated.Email)
}

func TestAccountServiceChangeEmailDisconnectsUser(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetEmail("old@example.com").SetPasswordHash(password).SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "new@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil)
	expectSessionRevoke(t, sessionStore, user.ID)

	hub := websocketmocks.NewMockPublisher(t)
	service.wsHub = hub
	disconnected := make(chan uuid.UUID, 1)
	hub.EXPECT().DisconnectUser(user.ID).Run(func(id uuid.UUID) { disconnected <- id }).Once()

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new@example.com", Code: "123456"})
	require.NoError(t, err)
	select {
	case id := <-disconnected:
		require.Equal(t, user.ID, id)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect")
	}
}

func TestAccountServiceDeleteAccountAnonymizesUserAndRevokesSessions(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	user := client.User.Create().
		SetUsername("account-user").
		SetEmail("user@example.com").
		SetFullName("Account User").
		SetBio("bio").
		SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	err := service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.NotNil(t, updated.DeletedAt)
	require.Nil(t, updated.Email)
	require.Nil(t, updated.Username)
	require.Nil(t, updated.FullName)
	require.Nil(t, updated.Bio)
}

func TestAccountServiceDeleteAccountRejectsOwnedGroup(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	user := client.User.Create().SetUsername("account-user").SaveX(ctx)
	chat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chat.ID).SetName("owned-group").SetInviteCode(uuid.NewString()).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(user.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	err := service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "transfer ownership")
}

func TestAccountServiceDeleteAccountRequiresPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)

	err = service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{})

	require.Error(t, err)
}

func TestAccountServiceDeleteAccountReturnsNotFoundForUnknownUser(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	err := service.DeleteAccount(context.Background(), uuid.New(), model.DeleteAccountRequest{})

	require.Error(t, err)
}

func TestAccountServiceDeleteAccountRejectsWrongPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)
	wrongPassword := "WrongPass1!"

	err = service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{Password: &wrongPassword})

	require.Error(t, err)
}

func TestAccountServiceDeleteAccountAcceptsCorrectPassword(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-user").SetPasswordHash(password).SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	err = service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{Password: stringPtr("OldPass1!")})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, user.ID)
	require.NotNil(t, updated.DeletedAt)
}

func TestAccountServiceDeleteAccountBroadcastsAndDisconnects(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	user := client.User.Create().SetUsername("account-user").SetEmail("user@example.com").SaveX(ctx)
	expectSessionRevoke(t, sessionStore, user.ID)

	hub := websocketmocks.NewMockPublisher(t)
	service.wsHub = hub
	events := make(chan string, 2)
	hub.EXPECT().BroadcastToContacts(user.ID, mock.Anything).Run(func(id uuid.UUID, event websocket.Event) {
		require.Equal(t, user.ID, id)
		events <- string(event.Type)
	}).Once()
	hub.EXPECT().DisconnectUser(user.ID).Run(func(id uuid.UUID) {
		require.Equal(t, user.ID, id)
		events <- "disconnect"
	}).Once()

	err := service.DeleteAccount(ctx, user.ID, model.DeleteAccountRequest{})
	require.NoError(t, err)
	seen := map[string]bool{}
	for range 2 {
		select {
		case eventType := <-events:
			seen[eventType] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for account deletion events")
		}
	}
	require.True(t, seen[string(websocket.EventUserDeleted)])
	require.True(t, seen["disconnect"])
}

func TestAccountServiceDeleteAccountReturnsSessionServiceError(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	u := client.User.Create().SetUsername("del-sess-err").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("redis down"))

	err := service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{})
	require.Error(t, err)
}

func TestAccountServiceChangeEmailUnlinksUserIdentities(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("account-ident").SetEmail("old-ident@example.com").SetPasswordHash(password).SaveX(ctx)

	client.UserIdentity.Create().SetUserID(user.ID).SetProvider(useridentity.ProviderGoogle).SetProviderID("google-sub-123").SaveX(ctx)
	require.True(t, client.UserIdentity.Query().Where(useridentity.UserID(user.ID)).ExistX(ctx))

	otpService.EXPECT().VerifyOTP(mock.Anything, "new-ident@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil)
	expectSessionRevoke(t, sessionStore, user.ID)

	disconnected := make(chan struct{}, 1)
	publisher.EXPECT().DisconnectUser(user.ID).Run(func(uuid.UUID) {
		disconnected <- struct{}{}
	}).Once()

	err = service.ChangeEmail(ctx, user.ID, model.ChangeEmailRequest{Email: "new-ident@example.com", Code: "123456"})
	require.NoError(t, err)

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("expected disconnect")
	}

	require.False(t, client.UserIdentity.Query().Where(useridentity.UserID(user.ID)).ExistX(ctx))
}

func TestAccountServiceChangePasswordRejectsMissingOrWrongOldPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	user := client.User.Create().SetUsername("pw-rej-user").SetPasswordHash(password).SaveX(ctx)

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		NewPassword: "NewPass1!",
	})
	require.Error(t, err)

	err = service.ChangePassword(ctx, user.ID, model.ChangePasswordRequest{
		OldPassword: stringPtr("WrongPass1!"),
		NewPassword: "NewPass1!",
	})
	require.Error(t, err)
}

func TestAccountServiceChangeEmailRejectsWhenNoPasswordSetOrSameEmail(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	userNoPw := client.User.Create().SetUsername("no-pw-email").SetEmail("nopw@example.com").SaveX(ctx)
	err := service.ChangeEmail(ctx, userNoPw.ID, model.ChangeEmailRequest{
		Email: "other@example.com",
		Code:  "123456",
	})
	require.Error(t, err)

	password, err := helper.HashPassword("Pass1234!")
	require.NoError(t, err)
	userWithPw := client.User.Create().SetUsername("same-email-u").SetEmail("same@example.com").SetPasswordHash(password).SaveX(ctx)
	err = service.ChangeEmail(ctx, userWithPw.ID, model.ChangeEmailRequest{
		Email: "same@example.com",
		Code:  "123456",
	})
	require.Error(t, err)
}

func TestAccountServiceChangePasswordWithoutOldPasswordWhenUserHasNoPassword(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	ctx := context.Background()
	u := client.User.Create().
		SetEmail("nopass@example.com").
		SetUsername("nopassuser").
		SetFullName("No Pass User").
		SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return("revoke-marker", nil).Once()

	disconnected := make(chan struct{}, 1)
	publisher.EXPECT().DisconnectUser(u.ID).Run(func(uuid.UUID) {
		disconnected <- struct{}{}
	}).Once()

	err := service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		NewPassword:     "NewSecurePass123!",
		ConfirmPassword: "NewSecurePass123!",
	})
	require.NoError(t, err)

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("expected disconnect")
	}

	updated := client.User.GetX(ctx, u.ID)
	require.NotNil(t, updated.PasswordHash)
	require.True(t, helper.CheckPasswordHash("NewSecurePass123!", *updated.PasswordHash))
}

func TestAccountServiceNotFoundAndConflictBranches(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	nonExistentID := uuid.New()
	err := service.ChangePassword(ctx, nonExistentID, model.ChangePasswordRequest{
		NewPassword:     "NewSecurePass123!",
		ConfirmPassword: "NewSecurePass123!",
	})
	require.Error(t, err)

	err = service.ChangeEmail(ctx, nonExistentID, model.ChangeEmailRequest{
		Email: "test@example.com",
		Code:  "123456",
	})
	require.Error(t, err)

	err = service.DeleteAccount(ctx, nonExistentID, model.DeleteAccountRequest{})
	require.Error(t, err)

	password, err := helper.HashPassword("Pass1234!")
	require.NoError(t, err)
	u1 := client.User.Create().SetUsername("u1-exist").SetEmail("u1@example.com").SetPasswordHash(password).SaveX(ctx)
	client.User.Create().SetUsername("u2-exist").SetEmail("u2@example.com").SetPasswordHash(password).SaveX(ctx)

	otpService.EXPECT().VerifyOTP(mock.Anything, "u2@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	err = service.ChangeEmail(ctx, u1.ID, model.ChangeEmailRequest{
		Email: "u2@example.com",
		Code:  "123456",
	})
	require.Error(t, err)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, mock.Anything).Return(helper.SessionRevokeSnapshot{Value: "marker"}, nil).Maybe()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, mock.Anything, mock.Anything).Return("newmarker", nil).Maybe()

	u3 := client.User.Create().SetUsername("u3-exist").SetEmail("u3@example.com").SetPasswordHash(password).SaveX(ctx)
	otpService.EXPECT().VerifyOTP(mock.Anything, "u3-new@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	err = service.ChangeEmail(ctx, u3.ID, model.ChangeEmailRequest{
		Email: "u3-new@example.com",
		Code:  "123456",
	})
	require.NoError(t, err)
}

func TestAccountServiceCancelledContextBranches(t *testing.T) {
	service, client, sessionStore, otpService := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	password, err := helper.HashPassword("Pass1234!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-cancel").SetEmail("cancel@example.com").SetPasswordHash(password).SaveX(context.Background())

	err = service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("Pass1234!"),
		NewPassword:     "NewPass1234!",
		ConfirmPassword: "NewPass1234!",
	})
	require.Error(t, err)

	otpService.EXPECT().VerifyOTP(mock.Anything, "new-cancel@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Maybe()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, mock.Anything).Return(helper.SessionRevokeSnapshot{}, nil).Maybe()

	err = service.ChangeEmail(ctx, u.ID, model.ChangeEmailRequest{
		Email: "new-cancel@example.com",
		Code:  "123456",
	})
	require.Error(t, err)

	err = service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{
		Password: stringPtr("Pass1234!"),
	})
	require.Error(t, err)
}

func TestAccountServiceDeleteAccountWithoutPasswordWhenUserHasNoPassword(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u := client.User.Create().SetUsername("u-nopass-del").SetEmail("nopass-del@example.com").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.AnythingOfType("int64")).Return("revoke-marker", nil).Once()

	err := service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{})
	require.NoError(t, err)

	deleted := client.User.GetX(ctx, u.ID)
	require.NotNil(t, deleted.DeletedAt)
	require.Nil(t, deleted.Email)
}

func TestAccountServiceDeleteAccountRejectsWhenUserOwnsGroups(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	owner := client.User.Create().SetUsername("owner-grp-del").SetEmail("owner-grp@example.com").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("owned grp").SetInviteCode("owned-code").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	err := service.DeleteAccount(ctx, owner.ID, model.DeleteAccountRequest{})
	require.Error(t, err)
}

func TestAccountServiceChangePasswordReturnsSessionServiceError(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-pass-sess-err").SetEmail("passsess@example.com").SetPasswordHash(password).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("redis unavailable"))

	err = service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     stringPtr("OldPass1!"),
		NewPassword:     "NewPass1!",
		ConfirmPassword: "NewPass1!",
	})
	require.Error(t, err)
}

func TestAccountServiceChangeEmailSnapshotError(t *testing.T) {
	service, client, sessionStore, otpMock := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	password, err := helper.HashPassword("OldPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-email-snap-err").SetEmail("old-snap@example.com").SetPasswordHash(password).SaveX(ctx)

	otpMock.EXPECT().VerifyOTP(mock.Anything, "new-snap@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("snapshot fail")).Once()

	err = service.ChangeEmail(ctx, u.ID, model.ChangeEmailRequest{
		Email: "new-snap@example.com",
		Code:  "123456",
	})
	require.Error(t, err)
}

func TestAccountServiceDeleteAccountSnapshotError(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	u := client.User.Create().SetUsername("u-del-snap-err").SetEmail("del-snap@example.com").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("snapshot fail")).Once()

	err := service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{})
	require.Error(t, err)
}

func TestAccountServiceChangePasswordNilOldPasswordAndWrongOldPassword(t *testing.T) {
	service, client, _, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	hash, err := helper.HashPassword("CurrentPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-change-nil-old").SetEmail("chgnil@example.com").SetPasswordHash(hash).SaveX(ctx)

	err = service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		NewPassword:     "NewPass123!",
		ConfirmPassword: "NewPass123!",
	})
	require.Error(t, err)

	wrongOld := "WrongOldPass1!"
	err = service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     &wrongOld,
		NewPassword:     "NewPass123!",
		ConfirmPassword: "NewPass123!",
	})
	require.Error(t, err)
}

func TestAccountServiceChangePasswordWhenNoPasswordWithOldPasswordProvided(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u := client.User.Create().SetUsername("u-chg-nopass-old").SetEmail("nopassold@example.com").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("marker-1", nil).Once()

	oldPass := "IgnoredOldPass1!"
	err := service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     &oldPass,
		NewPassword:     "NewPass123!",
		ConfirmPassword: "NewPass123!",
	})
	require.NoError(t, err)

	updated, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.PasswordHash)
}

func TestAccountServiceChangeEmailWhenUserHasNoEmail(t *testing.T) {
	service, client, sessionStore, otpMock := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	hash, err := helper.HashPassword("CurrentPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-chg-noemail").SetPasswordHash(hash).SaveX(ctx)

	otpMock.EXPECT().VerifyOTP(mock.Anything, "firstemail@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("marker-2", nil).Once()

	err = service.ChangeEmail(ctx, u.ID, model.ChangeEmailRequest{
		Email: "firstemail@example.com",
		Code:  "123456",
	})
	require.NoError(t, err)

	updated, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.Email)
	require.Equal(t, "firstemail@example.com", *updated.Email)
}

func TestAccountServiceDeleteAccountAllowsMemberAndAdminOfGroups(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	owner := client.User.Create().SetUsername("grp-real-owner").SaveX(ctx)
	member := client.User.Create().SetUsername("grp-member-del").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Some Group").SetInviteCode("grp-some-code").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, member.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, member.ID, mock.Anything).Return("marker-del", nil).Once()

	err := service.DeleteAccount(ctx, member.ID, model.DeleteAccountRequest{})
	require.NoError(t, err)

	deletedUser, err := client.User.Get(ctx, member.ID)
	require.NoError(t, err)
	require.NotNil(t, deletedUser.DeletedAt)
	require.Nil(t, deletedUser.Username)
	require.Nil(t, deletedUser.Email)
}

func TestAccountServiceChangePasswordFailsRevokeAllSessionsAt(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	hash, err := helper.HashPassword("CurrentPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-chg-revfail").SetPasswordHash(hash).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("", errors.New("redis down")).Once()

	oldPass := "CurrentPass1!"
	err = service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     &oldPass,
		NewPassword:     "NewPass123!",
		ConfirmPassword: "NewPass123!",
	})
	require.Error(t, err)
}

func TestAccountServiceChangeEmailFailsRevokeAllSessionsAt(t *testing.T) {
	service, client, sessionStore, otpMock := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	hash, err := helper.HashPassword("CurrentPass1!")
	require.NoError(t, err)
	u := client.User.Create().SetUsername("u-chg-em-revfail").SetPasswordHash(hash).SetEmail("oldem@example.com").SaveX(ctx)

	otpMock.EXPECT().VerifyOTP(mock.Anything, "newem@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("", errors.New("redis down")).Once()

	err = service.ChangeEmail(ctx, u.ID, model.ChangeEmailRequest{
		Email: "newem@example.com",
		Code:  "123456",
	})
	require.Error(t, err)
}

func TestAccountServiceDeleteAccountFailsRevokeAllSessionsAt(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	u := client.User.Create().SetUsername("u-del-revfail").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, u.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, u.ID, mock.Anything).Return("", errors.New("redis down")).Once()

	err := service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{})
	require.Error(t, err)
}

func TestAccountServiceOperationsWithWsHubNil(t *testing.T) {
	service, client, sessionStore, otpMock := newAccountServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	service.wsHub = nil

	pass, _ := helper.HashPassword("CurrentPass1!")
	u := client.User.Create().SetUsername("wsnil-user").SetEmail("wsnil@example.com").SetPasswordHash(pass).SaveX(ctx)

	expectSessionRevoke(t, sessionStore, u.ID)
	oldPass := "CurrentPass1!"
	err := service.ChangePassword(ctx, u.ID, model.ChangePasswordRequest{
		OldPassword:     &oldPass,
		NewPassword:     "NewPass123!",
		ConfirmPassword: "NewPass123!",
	})
	require.NoError(t, err)

	otpMock.EXPECT().VerifyOTP(mock.Anything, "wsnil2@example.com", "123456", string(constant.ModeChangeEmail)).Return(nil).Once()
	expectSessionRevoke(t, sessionStore, u.ID)
	err = service.ChangeEmail(ctx, u.ID, model.ChangeEmailRequest{
		Email: "wsnil2@example.com",
		Code:  "123456",
	})
	require.NoError(t, err)

	expectSessionRevoke(t, sessionStore, u.ID)
	p := "NewPass123!"
	err = service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{Password: &p})
	require.NoError(t, err)
}

func TestAccountServiceDeleteAccountWithUserIdentity(t *testing.T) {
	service, client, sessionStore, _ := newAccountServiceTest(t)
	ctx := context.Background()

	u := client.User.Create().
		SetEmail("identitydel@example.com").
		SetUsername("identitydel").
		SaveX(ctx)

	client.UserIdentity.Create().
		SetUserID(u.ID).
		SetProvider("google").
		SetProviderID("g-del-123").
		SaveX(ctx)

	expectSessionRevoke(t, sessionStore, u.ID)
	err := service.DeleteAccount(ctx, u.ID, model.DeleteAccountRequest{})
	require.NoError(t, err)

	identitiesCount := client.UserIdentity.Query().Where().CountX(ctx)
	require.Equal(t, 0, identitiesCount)
}

func stringPtr(value string) *string {
	return &value
}
