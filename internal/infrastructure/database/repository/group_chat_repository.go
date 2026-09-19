package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"fmt"
	"strconv"
	"strings"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

type GroupChatRepository struct {
	client *ent.Client
}

func NewGroupChatRepository(client *ent.Client) *GroupChatRepository {
	return &GroupChatRepository{
		client: client,
	}
}

func (r *GroupChatRepository) SearchPublicGroups(ctx context.Context, queryStr string, cursor string, limit int, sortBy string) ([]*ent.GroupChat, string, bool, error) {
	queryStr = strings.TrimSpace(queryStr)

	if sortBy == "member_count" {
		return r.searchPublicGroupsByMemberCount(ctx, queryStr, cursor, limit)
	}

	return r.searchPublicGroupsByName(ctx, queryStr, cursor, limit)
}

func (r *GroupChatRepository) searchPublicGroupsByName(ctx context.Context, queryStr string, cursor string, limit int) ([]*ent.GroupChat, string, bool, error) {
	query := r.client.GroupChat.Query().
		Where(
			groupchat.IsPublic(true),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		)

	if queryStr != "" {
		lowerQuery := strings.ToLower(queryStr)
		query = query.Where(
			groupchat.Or(
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(groupchat.FieldName)), lowerQuery))
				},
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(groupchat.FieldDescription)), lowerQuery))
				},
			),
		)
	}

	delimiter := "|||"

	if cursor != "" {
		cursorName, cursorIDStr, err := helper.DecodeCursor(cursor, delimiter)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor format: %w", err)
		}

		cursorID, err := uuid.Parse(cursorIDStr)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor id format: %w", err)
		}

		query = query.Where(
			groupchat.Or(
				groupchat.NameGT(cursorName),
				groupchat.And(
					groupchat.NameEQ(cursorName),
					groupchat.IDGT(cursorID),
				),
			),
		)
	}

	query = query.
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldName, groupchat.FieldDescription, groupchat.FieldAvatarID).
		Order(ent.Asc(groupchat.FieldName), ent.Asc(groupchat.FieldID)).
		Limit(limit + 1).
		WithAvatar()

	groups, err := query.All(ctx)
	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	var nextCursor string

	if len(groups) > limit {
		hasNext = true
		groups = groups[:limit]
		lastGroup := groups[len(groups)-1]

		nextCursor = helper.EncodeCursor(lastGroup.Name, lastGroup.ID.String(), delimiter)
	}

	return groups, nextCursor, hasNext, nil
}

func (r *GroupChatRepository) searchPublicGroupsByMemberCount(ctx context.Context, queryStr string, cursor string, limit int) ([]*ent.GroupChat, string, bool, error) {
	query := r.client.GroupChat.Query().
		Where(
			groupchat.IsPublic(true),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		)

	if queryStr != "" {
		lowerQuery := strings.ToLower(queryStr)
		query = query.Where(
			groupchat.Or(
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(groupchat.FieldName)), lowerQuery))
				},
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(groupchat.FieldDescription)), lowerQuery))
				},
			),
		)
	}

	delimiter := "|||"

	memberCountExpr := func(s *sql.Selector) string {
		return fmt.Sprintf(
			"(SELECT COUNT(*) FROM %s JOIN %s ON %s.%s = %s.%s WHERE %s.%s = %s.%s AND %s.%s IS NULL)",
			groupmember.Table,
			user.Table,
			groupmember.Table, groupmember.FieldUserID,
			user.Table, user.FieldID,
			groupmember.Table, groupmember.FieldGroupChatID,
			s.TableName(), groupchat.FieldID,
			user.Table, user.FieldDeletedAt,
		)
	}

	if cursor != "" {
		cursorCountStr, cursorIDStr, err := helper.DecodeCursor(cursor, delimiter)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor format: %w", err)
		}

		cursorCount, err := strconv.Atoi(cursorCountStr)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor count format: %w", err)
		}

		cursorID, err := uuid.Parse(cursorIDStr)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor id format: %w", err)
		}

		query = query.Where(func(s *sql.Selector) {
			countExpr := memberCountExpr(s)
			s.Where(sql.Or(
				sql.P(func(b *sql.Builder) {
					b.WriteString(countExpr).WriteString(" < ").Arg(cursorCount)
				}),
				sql.And(
					sql.P(func(b *sql.Builder) {
						b.WriteString(countExpr).WriteString(" = ").Arg(cursorCount)
					}),
					sql.GT(s.C(groupchat.FieldID), cursorID),
				),
			))
		})
	}

	query = query.
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldName, groupchat.FieldDescription, groupchat.FieldAvatarID).
		Modify(func(s *sql.Selector) {
			countExpr := memberCountExpr(s)
			s.OrderExpr(sql.Expr(countExpr + " DESC"))
			s.OrderExpr(sql.Expr(s.C(groupchat.FieldID) + " ASC"))
		}).
		Limit(limit + 1).
		WithAvatar()

	groups, err := query.All(ctx)
	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	var nextCursor string

	if len(groups) > limit {
		hasNext = true
		groups = groups[:limit]
	}

	if hasNext && len(groups) > 0 {
		lastGroup := groups[len(groups)-1]

		counts, countErr := r.client.GroupMember.Query().
			Where(
				groupmember.GroupChatID(lastGroup.ID),
				groupmember.HasUserWith(user.DeletedAtIsNil()),
			).
			Count(ctx)
		if countErr != nil {
			return nil, "", false, fmt.Errorf("failed to count members for cursor: %w", countErr)
		}

		nextCursor = helper.EncodeCursor(strconv.Itoa(counts), lastGroup.ID.String(), delimiter)
	}

	return groups, nextCursor, hasNext, nil
}
