package helper

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"testing"
	"time"

	"github.com/google/uuid"
)

type testURLGenerator struct{}

func (testURLGenerator) GetPublicURL(path string) string {
	return "https://cdn.test/" + path
}

func (testURLGenerator) GetPresignedURL(path string, _ time.Duration) (string, error) {
	return "https://signed.test/" + path, nil
}

func TestMapChatToResponsePrivateChatForUser1(t *testing.T) {
	userID := uuid.New()
	otherID := uuid.New()
	chatID := uuid.New()
	readAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	hiddenAt := readAt.Add(time.Hour)
	name := "Other User"

	response := MapChatToResponse(userID, &ent.Chat{
		ID:   chatID,
		Type: chat.TypePrivate,
		Edges: ent.ChatEdges{PrivateChat: &ent.PrivateChat{
			User1ID:          &userID,
			User2ID:          &otherID,
			User1LastReadAt:  &readAt,
			User2LastReadAt:  &hiddenAt,
			User1HiddenAt:    &hiddenAt,
			User1UnreadCount: 3,
			User2UnreadCount: 7,
			Edges: ent.PrivateChatEdges{User2: &ent.User{
				ID:       otherID,
				FullName: &name,
				Edges:    ent.UserEdges{Avatar: &ent.Media{FileName: "avatar.png"}},
			}},
		}},
	}, map[uuid.UUID]BlockStatus{otherID: {BlockedByMe: true}}, map[uuid.UUID]bool{otherID: true}, testURLGenerator{})

	if response.ID != chatID || response.Name != name || response.UnreadCount != 3 {
		t.Fatalf("unexpected private response: %+v", response)
	}
	if response.OtherUserID == nil || *response.OtherUserID != otherID {
		t.Fatalf("expected other user id %s, got %v", otherID, response.OtherUserID)
	}
	if response.IsOnline || !response.IsBlockedByMe || response.Avatar != "https://cdn.test/avatar.png" {
		t.Fatalf("unexpected private status/avatar: %+v", response)
	}
	if response.LastReadAt == nil || response.OtherLastReadAt == nil || response.HiddenAt == nil {
		t.Fatalf("expected timestamps: %+v", response)
	}
}

func TestMapChatToResponsePrivateChatDeletedAndBannedUsers(t *testing.T) {
	userID := uuid.New()
	deletedID := uuid.New()
	deletedAt := time.Now().UTC()

	deleted := MapChatToResponse(userID, &ent.Chat{
		Type: chat.TypePrivate,
		Edges: ent.ChatEdges{PrivateChat: &ent.PrivateChat{
			User1ID: &userID,
			User2ID: &deletedID,
			Edges:   ent.PrivateChatEdges{User2: &ent.User{ID: deletedID, DeletedAt: &deletedAt}},
		}},
	}, nil, map[uuid.UUID]bool{deletedID: true}, testURLGenerator{})
	if deleted.Name != "Deleted User" || !deleted.OtherUserIsDeleted || deleted.IsOnline {
		t.Fatalf("unexpected deleted user response: %+v", deleted)
	}

	bannedUntil := time.Now().UTC().Add(time.Hour)
	bannedName := "Banned User"
	banned := MapChatToResponse(userID, &ent.Chat{
		Type: chat.TypePrivate,
		Edges: ent.ChatEdges{PrivateChat: &ent.PrivateChat{
			User1ID: &userID,
			User2ID: &deletedID,
			Edges: ent.PrivateChatEdges{User2: &ent.User{
				ID:          deletedID,
				FullName:    &bannedName,
				IsBanned:    true,
				BannedUntil: &bannedUntil,
			}},
		}},
	}, nil, map[uuid.UUID]bool{deletedID: true}, testURLGenerator{})
	if banned.Name != bannedName || !banned.OtherUserIsBanned || banned.IsOnline {
		t.Fatalf("unexpected banned user response: %+v", banned)
	}
}

func TestMapChatToResponseGroupChat(t *testing.T) {
	chatID := uuid.New()
	memberID := uuid.New()
	name := "Group"
	description := "Description"
	expiresAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	lastReadAt := expiresAt.Add(time.Hour)

	response := MapChatToResponse(uuid.New(), &ent.Chat{
		ID:   chatID,
		Type: chat.TypeGroup,
		Edges: ent.ChatEdges{GroupChat: &ent.GroupChat{
			Name:            name,
			Description:     &description,
			IsPublic:        true,
			InviteCode:      "invite",
			InviteExpiresAt: &expiresAt,
			Edges: ent.GroupChatEdges{Members: []*ent.GroupMember{{
				UserID:      memberID,
				Role:        groupmember.RoleAdmin,
				UnreadCount: 4,
				LastReadAt:  &lastReadAt,
			}}},
		}},
	}, nil, nil, testURLGenerator{})

	if response.ID != chatID || response.Name != name || response.Description == nil || *response.Description != description {
		t.Fatalf("unexpected group response: %+v", response)
	}
	if response.IsPublic == nil || !*response.IsPublic || response.InviteCode == nil || response.MyRole == nil || *response.MyRole != "admin" {
		t.Fatalf("unexpected group metadata: %+v", response)
	}
	if response.UnreadCount != 4 || response.LastReadAt == nil || response.InviteExpiresAt == nil {
		t.Fatalf("unexpected group member state: %+v", response)
	}
}

func TestMapChatToResponsePrivateChatForUser2AndUnblockedOnlineUser(t *testing.T) {
	userID := uuid.New()
	otherID := uuid.New()
	readAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	name := "Current User"

	response := MapChatToResponse(userID, &ent.Chat{
		ID:   uuid.New(),
		Type: chat.TypePrivate,
		Edges: ent.ChatEdges{PrivateChat: &ent.PrivateChat{
			User1ID:          &otherID,
			User2ID:          &userID,
			User1LastReadAt:  &readAt,
			User2LastReadAt:  &readAt,
			User2UnreadCount: 5,
			Edges: ent.PrivateChatEdges{User1: &ent.User{
				ID:       otherID,
				FullName: &name,
			}},
		}},
	}, nil, map[uuid.UUID]bool{otherID: true}, testURLGenerator{})

	if response.UnreadCount != 5 || !response.IsOnline || response.OtherUserID == nil || *response.OtherUserID != otherID || response.LastReadAt == nil || response.OtherLastReadAt == nil {
		t.Fatalf("unexpected user2 private response: %+v", response)
	}
}

func TestMapChatToResponsePublicGroupMemberInviteExpiry(t *testing.T) {
	memberID := uuid.New()
	expiresAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	response := MapChatToResponse(uuid.New(), &ent.Chat{
		ID:   uuid.New(),
		Type: chat.TypeGroup,
		Edges: ent.ChatEdges{GroupChat: &ent.GroupChat{
			Name:            "Public",
			IsPublic:        true,
			InviteCode:      "public-invite",
			InviteExpiresAt: &expiresAt,
			Edges:           ent.GroupChatEdges{Members: []*ent.GroupMember{{UserID: memberID, Role: groupmember.RoleMember}}},
		}},
	}, nil, nil, testURLGenerator{})

	if response.InviteCode == nil || *response.InviteCode != "public-invite" || response.InviteExpiresAt != nil {
		t.Fatalf("unexpected public group invite fields: %+v", response)
	}
}
