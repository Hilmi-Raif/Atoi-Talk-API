package helper

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/internal/model"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestToGroupMemberDTO(t *testing.T) {
	memberID := uuid.New()
	userID := uuid.New()
	fullName := "Alice"
	username := "alice"
	joinedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	got := ToGroupMemberDTO(&ent.GroupMember{
		ID:       memberID,
		Role:     groupmember.RoleAdmin,
		JoinedAt: joinedAt,
		Edges: ent.GroupMemberEdges{User: &ent.User{
			ID:       userID,
			FullName: &fullName,
			Username: &username,
			IsBanned: true,
			Edges:    ent.UserEdges{Avatar: &ent.Media{FileName: "avatar.webp"}},
		}},
	}, testURLGenerator{})

	if got.ID != memberID || got.UserID != userID || got.Username != username || got.FullName != fullName {
		t.Fatalf("unexpected group member dto: %+v", got)
	}
	if got.Avatar != "https://cdn.test/avatar.webp" || got.Role != "admin" || got.JoinedAt != joinedAt.String() || !got.IsBanned {
		t.Fatalf("unexpected group member details: %+v", got)
	}

	expired := time.Now().UTC().Add(-time.Hour)
	got = ToGroupMemberDTO(&ent.GroupMember{Edges: ent.GroupMemberEdges{User: &ent.User{
		ID:          userID,
		IsBanned:    true,
		BannedUntil: &expired,
	}}}, testURLGenerator{})
	if got.IsBanned {
		t.Fatal("expired ban should not be reported as active")
	}
}

func TestToGroupMemberDTOHandlesMissingEntity(t *testing.T) {
	if got := ToGroupMemberDTO(nil, testURLGenerator{}); !reflect.DeepEqual(got, model.GroupMemberDTO{}) {
		t.Fatalf("expected zero DTO for nil member, got %+v", got)
	}
}
