package group

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/constant"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"log/slog"

	"entgo.io/ent/dialect/sql"
)

func lockGroupMembership(ctx context.Context, tx *ent.Tx, gc *ent.GroupChat) error {
	// A no-op update holds the group row lock until commit without overwriting its name.
	if err := tx.GroupChat.UpdateOneID(gc.ID).SetName(gc.Name).Modify(func(u *sql.UpdateBuilder) {
		u.Set(groupchat.FieldName, sql.Expr(groupchat.FieldName))
	}).Exec(ctx); err != nil {
		slog.Error("Failed to lock group for membership update", "error", err)
		return helper.NewInternalServerError("")
	}
	return nil
}

func checkMemberCapacity(ctx context.Context, tx *ent.Tx, gc *ent.GroupChat, adding int) error {
	count, err := tx.GroupMember.Query().Where(
		groupmember.GroupChatID(gc.ID),
		groupmember.HasUserWith(user.DeletedAtIsNil()),
	).Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members", "error", err)
		return helper.NewInternalServerError("")
	}
	if count+adding > constant.MaxGroupMembers {
		return helper.NewConflictError("Group is full")
	}
	return nil
}
