package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

type UserRepository struct {
	client *ent.Client
}

func NewUserRepository(client *ent.Client) *UserRepository {
	return &UserRepository{
		client: client,
	}
}

func (r *UserRepository) SearchUsers(ctx context.Context, currentUserID uuid.UUID, queryStr string, cursor string, limit int, excludeChatID *uuid.UUID) ([]*ent.User, string, bool, error) {
	queryStr = strings.TrimSpace(queryStr)

	query := r.client.User.Query().
		Where(
			user.IDNEQ(currentUserID),
			user.DeletedAtIsNil(),

			user.Or(
				user.IsBanned(false),
				user.And(
					user.IsBanned(true),
					user.BannedUntilLT(time.Now().UTC()),
				),
			),

			func(s *sql.Selector) {
				t := sql.Table(userblock.Table)
				s.Where(
					sql.Not(
						sql.Exists(
							sql.Select(userblock.FieldID).From(t).Where(
								sql.Or(
									sql.And(
										sql.EQ(t.C(userblock.FieldBlockerID), currentUserID),
										sql.ColumnsEQ(t.C(userblock.FieldBlockedID), s.C(user.FieldID)),
									),
									sql.And(
										sql.ColumnsEQ(t.C(userblock.FieldBlockerID), s.C(user.FieldID)),
										sql.EQ(t.C(userblock.FieldBlockedID), currentUserID),
									),
								),
							),
						),
					),
				)
			},
		)

	if excludeChatID != nil {
		query = query.Where(
			func(s *sql.Selector) {
				gm := sql.Table(groupmember.Table)
				gc := sql.Table(groupchat.Table)
				s.Where(
					sql.Not(
						sql.Exists(
							sql.Select(gm.C(groupmember.FieldID)).
								From(gm).
								Join(gc).On(gm.C(groupmember.FieldGroupChatID), gc.C(groupchat.FieldID)).
								Where(
									sql.And(
										sql.EQ(gc.C(groupchat.FieldChatID), *excludeChatID),
										sql.ColumnsEQ(gm.C(groupmember.FieldUserID), s.C(user.FieldID)),
									),
								),
						),
					),
				)
			},
		)
	}

	lowerQuery := strings.ToLower(queryStr)
	query = query.Where(
		user.Or(
			func(s *sql.Selector) {
				s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldFullName)), lowerQuery))
			},
			user.EmailEqualFold(queryStr),
			func(s *sql.Selector) {
				s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldUsername)), lowerQuery))
			},
		),
	)

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
			user.Or(
				user.FullNameGT(cursorName),
				user.And(
					user.FullNameEQ(cursorName),
					user.IDGT(cursorID),
				),
			),
		)
	}

	query = query.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID).
		Order(ent.Asc(user.FieldFullName), ent.Asc(user.FieldID)).
		Limit(limit + 1).
		WithAvatar()

	users, err := query.All(ctx)
	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	var nextCursor string

	if len(users) > limit {
		hasNext = true
		users = users[:limit]
		lastUser := users[len(users)-1]

		fullName := ""
		if lastUser.FullName != nil {
			fullName = *lastUser.FullName
		}
		nextCursor = helper.EncodeCursor(fullName, lastUser.ID.String(), delimiter)
	}

	return users, nextCursor, hasNext, nil
}

func (r *UserRepository) GetBlockedUsers(ctx context.Context, currentUserID uuid.UUID, queryStr string, cursor string, limit int) ([]*ent.User, string, bool, error) {
	query := r.client.User.Query().
		Where(
			user.HasBlockedByRelWith(userblock.BlockerID(currentUserID)),
			user.DeletedAtIsNil(),
		)

	queryStr = strings.TrimSpace(queryStr)

	if queryStr != "" {

		lowerQuery := strings.ToLower(queryStr)
		query = query.Where(
			user.Or(
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldFullName)), lowerQuery))
				},
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldUsername)), lowerQuery))
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
			user.Or(
				user.FullNameGT(cursorName),
				user.And(
					user.FullNameEQ(cursorName),
					user.IDGT(cursorID),
				),
			),
		)
	}

	query = query.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID).
		Order(ent.Asc(user.FieldFullName), ent.Asc(user.FieldID)).
		Limit(limit + 1).
		WithAvatar()

	users, err := query.All(ctx)
	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	var nextCursor string

	if len(users) > limit {
		hasNext = true
		users = users[:limit]
		lastUser := users[len(users)-1]

		fullName := ""
		if lastUser.FullName != nil {
			fullName = *lastUser.FullName
		}
		nextCursor = helper.EncodeCursor(fullName, lastUser.ID.String(), delimiter)
	}

	return users, nextCursor, hasNext, nil
}
