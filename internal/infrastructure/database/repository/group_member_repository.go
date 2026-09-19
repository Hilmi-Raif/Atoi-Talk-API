package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

type GroupMemberRepository struct {
	client     *ent.Client
	countCache sync.Map
}

type cachedMemberCount struct {
	count     int
	expiresAt time.Time
}

const groupMemberCountCacheTTL = 10 * time.Second

type GroupMemberCount struct {
	GroupChatID uuid.UUID `json:"group_chat_id"`
	Count       int       `json:"count"`
}

func NewGroupMemberRepository(client *ent.Client) *GroupMemberRepository {
	return &GroupMemberRepository{
		client: client,
	}
}

func (r *GroupMemberRepository) CountActiveMembersByGroupIDs(ctx context.Context, groupIDs ...uuid.UUID) (map[uuid.UUID]int, error) {
	counts := make(map[uuid.UUID]int, len(groupIDs))
	if len(groupIDs) == 0 {
		return counts, nil
	}

	var missingIDs []uuid.UUID
	now := time.Now()
	for _, id := range groupIDs {
		if val, ok := r.countCache.Load(id); ok {
			if entry, ok := val.(cachedMemberCount); ok && now.Before(entry.expiresAt) {
				counts[id] = entry.count
				continue
			}
			r.countCache.Delete(id)
		}
		missingIDs = append(missingIDs, id)
	}

	if len(missingIDs) == 0 {
		return counts, nil
	}

	var rows []GroupMemberCount
	err := r.client.GroupMember.Query().
		Where(
			groupmember.GroupChatIDIn(missingIDs...),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		GroupBy(groupmember.FieldGroupChatID).
		Aggregate(ent.Count()).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	expiresAt := now.Add(groupMemberCountCacheTTL)
	for _, row := range rows {
		counts[row.GroupChatID] = row.Count
		r.countCache.Store(row.GroupChatID, cachedMemberCount{
			count:     row.Count,
			expiresAt: expiresAt,
		})
	}

	for _, id := range missingIDs {
		if _, exists := counts[id]; !exists {
			counts[id] = 0
			r.countCache.Store(id, cachedMemberCount{
				count:     0,
				expiresAt: expiresAt,
			})
		}
	}

	return counts, nil
}

func (r *GroupMemberRepository) InvalidateGroupMemberCount(groupID uuid.UUID) {
	r.countCache.Delete(groupID)
}

func (r *GroupMemberRepository) SearchGroupMembers(ctx context.Context, groupID uuid.UUID, query, cursor string, limit int) ([]*ent.GroupMember, string, bool, error) {
	query = strings.TrimSpace(query)

	queryBuilder := r.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(groupID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Order(ent.Asc(groupmember.FieldJoinedAt), ent.Asc(groupmember.FieldID)).
		Limit(limit + 1).
		WithUser(func(uq *ent.UserQuery) {

			uq.WithAvatar()
		})

	if query != "" {

		lowerQuery := strings.ToLower(query)
		queryBuilder = queryBuilder.Where(groupmember.HasUserWith(
			user.Or(
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldUsername)), lowerQuery))
				},
				func(s *sql.Selector) {
					s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldFullName)), lowerQuery))
				},
			),
		))
	}

	if cursor != "" {
		joinedAtStr, idStr, err := helper.DecodeCursor(cursor, "|")
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor format: %w", err)
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor id format: %w", err)
		}

		joinedAt, err := time.Parse(time.RFC3339Nano, joinedAtStr)
		if err != nil {
			return nil, "", false, fmt.Errorf("invalid cursor time format: %w", err)
		}

		queryBuilder = queryBuilder.Where(
			groupmember.Or(
				groupmember.JoinedAtGT(joinedAt),
				groupmember.And(
					groupmember.JoinedAtEQ(joinedAt),
					groupmember.IDGT(id),
				),
			),
		)
	}

	members, err := queryBuilder.All(ctx)
	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	if len(members) > limit {
		hasNext = true
		members = members[:limit]
	}

	var nextCursor string
	if hasNext && len(members) > 0 {
		lastMember := members[len(members)-1]
		nextCursor = helper.EncodeCursor(lastMember.JoinedAt.Format(time.RFC3339Nano), lastMember.ID.String(), "|")
	}

	return members, nextCursor, hasNext, nil
}
