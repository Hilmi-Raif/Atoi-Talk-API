package application

import (
	"AtoiTalkAPI/internal/domain/model"
	"context"

	"github.com/google/uuid"
)

type AuthVerifier interface {
	VerifyUser(context.Context, string) (*model.UserDTO, error)
}

type ChatServicePort interface {
	GetChats(context.Context, uuid.UUID, model.GetChatsRequest) ([]model.ChatListResponse, string, bool, error)
	GetChatByID(context.Context, uuid.UUID, uuid.UUID) (*model.ChatListResponse, error)
	MarkAsRead(context.Context, uuid.UUID, uuid.UUID) error
	HideChat(context.Context, uuid.UUID, uuid.UUID) error
}

type AuthServicePort interface {
	Login(context.Context, model.LoginRequest) (*model.AuthResponse, error)
	Logout(context.Context, string) error
	BeginGoogleAuth(context.Context) (*model.GoogleAuthInitResponse, error)
	GoogleExchange(context.Context, model.GoogleLoginRequest) (*model.AuthResponse, error)
	Register(context.Context, model.RegisterUserRequest) (*model.AuthResponse, error)
	ResetPassword(context.Context, model.ResetPasswordRequest) error
}

type AccountServicePort interface {
	ChangePassword(context.Context, uuid.UUID, model.ChangePasswordRequest) error
	ChangeEmail(context.Context, uuid.UUID, model.ChangeEmailRequest) error
	DeleteAccount(context.Context, uuid.UUID, model.DeleteAccountRequest) error
}

type OTPServicePort interface {
	SendOTP(context.Context, model.SendOTPRequest) error
}

type UserServicePort interface {
	GetCurrentUser(context.Context, uuid.UUID) (*model.UserDTO, error)
	GetUserProfile(context.Context, uuid.UUID, uuid.UUID) (*model.UserDTO, error)
	UpdateProfile(context.Context, uuid.UUID, model.UpdateProfileRequest) (*model.UserDTO, error)
	SearchUsers(context.Context, uuid.UUID, model.SearchUserRequest) ([]model.UserDTO, string, bool, error)
	GetBlockedUsers(context.Context, uuid.UUID, model.GetBlockedUsersRequest) ([]model.UserDTO, string, bool, error)
	BlockUser(context.Context, uuid.UUID, uuid.UUID) error
	UnblockUser(context.Context, uuid.UUID, uuid.UUID) error
}

type PrivateChatServicePort interface {
	CreatePrivateChat(context.Context, uuid.UUID, model.CreatePrivateChatRequest) (*model.ChatResponse, error)
}

type GroupChatServicePort interface {
	CreateGroupChat(context.Context, uuid.UUID, model.CreateGroupChatRequest) (*model.ChatListResponse, error)
	UpdateGroupChat(context.Context, uuid.UUID, uuid.UUID, model.UpdateGroupChatRequest, bool) (*model.ChatListResponse, error)
	SearchGroupMembers(context.Context, uuid.UUID, model.SearchGroupMembersRequest, bool) ([]model.GroupMemberDTO, string, bool, error)
	AddMember(context.Context, uuid.UUID, uuid.UUID, model.AddGroupMemberRequest) ([]*model.MessageResponse, error)
	LeaveGroup(context.Context, uuid.UUID, uuid.UUID) (*model.MessageResponse, error)
	KickMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*model.MessageResponse, error)
	UpdateMemberRole(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, model.UpdateGroupMemberRoleRequest) (*model.MessageResponse, error)
	TransferOwnership(context.Context, uuid.UUID, uuid.UUID, model.TransferGroupOwnershipRequest) (*model.MessageResponse, error)
	DeleteGroup(context.Context, uuid.UUID, uuid.UUID, bool) error
	SearchPublicGroups(context.Context, uuid.UUID, model.SearchPublicGroupsRequest) ([]model.PublicGroupDTO, string, bool, error)
	JoinPublicGroup(context.Context, uuid.UUID, uuid.UUID) (*model.ChatListResponse, error)
	JoinGroupByInvite(context.Context, uuid.UUID, string) (*model.ChatListResponse, error)
	GetGroupByInviteCode(context.Context, string) (*model.GroupPreviewDTO, error)
	ResetInviteCode(context.Context, uuid.UUID, uuid.UUID) (*model.GroupInviteResponse, error)
}

type MessageServicePort interface {
	SendMessage(context.Context, uuid.UUID, model.SendMessageRequest) (*model.MessageResponse, error)
	EditMessage(context.Context, uuid.UUID, uuid.UUID, model.EditMessageRequest) (*model.MessageResponse, error)
	GetMessages(context.Context, uuid.UUID, model.GetMessagesRequest) ([]model.MessageResponse, string, bool, string, bool, error)
	DeleteMessage(context.Context, uuid.UUID, uuid.UUID) error
}

type MediaServicePort interface {
	UploadMedia(context.Context, uuid.UUID, model.UploadMediaRequest) (*model.UploadMediaResponse, error)
	CompleteUpload(context.Context, uuid.UUID, uuid.UUID) (*model.MediaDTO, error)
	GetMediaURL(context.Context, uuid.UUID, uuid.UUID) (*model.MediaURLResponse, error)
}

type ReportServicePort interface {
	CreateReport(context.Context, uuid.UUID, model.CreateReportRequest) error
}

type AdminServicePort interface {
	BanUser(context.Context, uuid.UUID, model.BanUserRequest) error
	UnbanUser(context.Context, uuid.UUID, uuid.UUID) error
	GetReports(context.Context, model.GetReportsRequest) ([]model.ReportListResponse, string, bool, error)
	GetReportDetail(context.Context, uuid.UUID) (*model.ReportDetailResponse, error)
	ResolveReport(context.Context, uuid.UUID, uuid.UUID, model.ResolveReportRequest) error
	DeleteReport(context.Context, uuid.UUID) error
	GetDashboardStats(context.Context) (*model.DashboardStatsResponse, error)
	GetUsers(context.Context, model.AdminGetUserListRequest) ([]model.AdminUserListResponse, string, bool, error)
	GetUserDetail(context.Context, uuid.UUID) (*model.AdminUserDetailResponse, error)
	ResetUserInfo(context.Context, model.ResetUserInfoRequest) error
	GetGroups(context.Context, model.AdminGetGroupListRequest) ([]model.AdminGroupListResponse, string, bool, error)
	GetGroupDetail(context.Context, uuid.UUID) (*model.AdminGroupDetailResponse, error)
}
