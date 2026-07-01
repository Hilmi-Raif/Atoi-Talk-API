package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"log/slog"

	"github.com/google/uuid"
)

func (s *GroupChatService) SearchGroupMembers(ctx context.Context, userID uuid.UUID, req model.SearchGroupMembersRequest, isAdmin bool) ([]model.GroupMemberDTO, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	gc, err := s.client.GroupChat.Query().
		Where(
			groupchat.ChatID(req.GroupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", false, helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	if !isAdmin {
		isMember, err := s.client.GroupMember.Query().
			Where(
				groupmember.GroupChatID(gc.ID),
				groupmember.UserID(userID),
			).
			Exist(ctx)
		if err != nil {
			slog.Error("Failed to check group membership", "error", err)
			return nil, "", false, helper.NewInternalServerError("")
		}
		if !isMember {
			return nil, "", false, helper.NewForbiddenError("You are not a member of this group")
		}
	}

	members, nextCursor, hasNext, err := s.repo.GroupMember.SearchGroupMembers(ctx, gc.ID, req.Query, req.Cursor, req.Limit)
	if err != nil {
		slog.Error("Failed to search group members", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	memberDTOs := make([]model.GroupMemberDTO, 0)
	for _, m := range members {

		if m.Edges.User != nil && m.Edges.User.DeletedAt != nil {
			continue
		}
		memberDTOs = append(memberDTOs, helper.ToGroupMemberDTO(m, s.storageAdapter))
	}

	return memberDTOs, nextCursor, hasNext, nil
}
