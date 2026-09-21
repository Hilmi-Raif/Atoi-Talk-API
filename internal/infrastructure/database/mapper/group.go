package mapper

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/domain/model"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"time"
)

func ToGroupMemberDTO(m *ent.GroupMember, urlGen objectstorage.URLGenerator) model.GroupMemberDTO {
	if m == nil || m.Edges.User == nil {
		return model.GroupMemberDTO{}
	}

	user := m.Edges.User
	avatarURL := ""
	if user.Edges.Avatar != nil {
		avatarURL = urlGen.GetPublicURL(user.Edges.Avatar.FileName)
	}

	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}

	username := ""
	if user.Username != nil {
		username = *user.Username
	}

	isBanned := user.IsBanned
	if isBanned && user.BannedUntil != nil && time.Now().UTC().After(*user.BannedUntil) {
		isBanned = false
	}

	return model.GroupMemberDTO{
		ID:       m.ID,
		UserID:   user.ID,
		Username: username,
		FullName: fullName,
		Avatar:   avatarURL,
		Role:     string(m.Role),
		JoinedAt: m.JoinedAt.String(),
		IsBanned: isBanned,
	}
}
