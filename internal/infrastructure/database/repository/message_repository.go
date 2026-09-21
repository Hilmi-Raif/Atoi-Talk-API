package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

var ErrMessageNotFound = errors.New("message not found")

type MessageAroundPage struct {
	Messages []*ent.Message
	HasOlder bool
	HasNewer bool
}

func runAroundQueries(ctx context.Context, previousQuery, nextQuery func(context.Context) ([]*ent.Message, error)) (prev []*ent.Message, next []*ent.Message, err error) {
	prev, err = previousQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	next, err = nextQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	return prev, next, nil
}

type MessageRepository struct {
	client *ent.Client
}

func NewMessageRepository(client *ent.Client) *MessageRepository {
	return &MessageRepository{
		client: client,
	}
}

func (r *MessageRepository) GetMessages(ctx context.Context, chatID uuid.UUID, hiddenAt *time.Time, cursor uuid.UUID, limit int, direction string) ([]*ent.Message, error) {
	query := r.client.Message.Query().
		Where(message.ChatID(chatID))

	if hiddenAt != nil {
		query = query.Where(message.CreatedAtGT(*hiddenAt))
	}

	if direction == "newer" {
		query = query.Order(ent.Asc(message.FieldID))
		if cursor != uuid.Nil {
			query = query.Where(message.IDGT(cursor))
		}
	} else {
		query = query.Order(ent.Desc(message.FieldID))
		if cursor != uuid.Nil {
			query = query.Where(message.IDLT(cursor))
		}
	}

	query = query.Limit(limit + 1).
		WithSender(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt)
			q.WithAvatar()
		}).
		WithAttachments().
		WithReplyTo(func(q *ent.MessageQuery) {
			q.WithSender(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt)
				uq.WithAvatar()
			})
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Limit(1)
			})
		})

	return query.All(ctx)
}

func (r *MessageRepository) hydrateMessageRelations(ctx context.Context, messages ...*ent.Message) (map[uuid.UUID]*ent.Message, error) {
	ids := make([]uuid.UUID, 0, len(messages))
	seen := make(map[uuid.UUID]struct{}, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if _, ok := seen[msg.ID]; ok {
			continue
		}
		seen[msg.ID] = struct{}{}
		ids = append(ids, msg.ID)
	}
	if len(ids) == 0 {
		return map[uuid.UUID]*ent.Message{}, nil
	}

	loaded, err := r.client.Message.Query().
		Where(message.IDIn(ids...)).
		WithSender(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt)
			q.WithAvatar()
		}).
		WithAttachments().
		WithReplyTo(func(q *ent.MessageQuery) {
			q.WithSender(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt)
				uq.WithAvatar()
			})
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Limit(1)
			})
		}).
		All(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[uuid.UUID]*ent.Message, len(loaded))
	for _, msg := range loaded {
		byID[msg.ID] = msg
	}
	return byID, nil
}

func (r *MessageRepository) GetMessagesAround(ctx context.Context, chatID uuid.UUID, hiddenAt *time.Time, aroundID uuid.UUID, limit int) ([]*ent.Message, error) {
	page, err := r.GetMessagesAroundPage(ctx, chatID, hiddenAt, aroundID, limit)
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

func (r *MessageRepository) GetMessagesAroundPage(ctx context.Context, chatID uuid.UUID, hiddenAt *time.Time, aroundID uuid.UUID, limit int) (*MessageAroundPage, error) {
	halfLimit := limit / 2

	prevQuery := r.client.Message.Query().
		Where(
			message.ChatID(chatID),
			message.IDLTE(aroundID),
		)
	if hiddenAt != nil {
		prevQuery = prevQuery.Where(message.CreatedAtGT(*hiddenAt))
	}
	nextQuery := r.client.Message.Query().
		Where(
			message.ChatID(chatID),
			message.IDGT(aroundID),
		)
	if hiddenAt != nil {
		nextQuery = nextQuery.Where(message.CreatedAtGT(*hiddenAt))
	}

	prevWindow, nextMsgs, err := runAroundQueries(ctx,
		func(ctx context.Context) ([]*ent.Message, error) {
			return prevQuery.
				Order(ent.Desc(message.FieldID)).
				Limit(halfLimit + 2).
				All(ctx)
		},
		func(ctx context.Context) ([]*ent.Message, error) {
			return nextQuery.
				Order(ent.Asc(message.FieldID)).
				Limit(halfLimit + 1).
				All(ctx)
		},
	)
	if err != nil {
		return nil, err
	}

	if len(prevWindow) == 0 || prevWindow[0].ID != aroundID {
		if hiddenAt != nil {
			exists, existsErr := r.client.Message.Query().
				Where(message.ID(aroundID), message.ChatID(chatID)).
				Exist(ctx)
			if existsErr != nil {
				return nil, existsErr
			}
			if exists {
				return nil, ErrMessageNotFound
			}
		}
		return nil, &ent.NotFoundError{}
	}

	targetMsg := prevWindow[0]
	prevMsgs := prevWindow[1:]

	hasOlder := len(prevMsgs) > halfLimit
	hasNewer := len(nextMsgs) > halfLimit
	if hasOlder {
		prevMsgs = prevMsgs[:halfLimit]
	}
	if hasNewer {
		nextMsgs = nextMsgs[:halfLimit]
	}

	window := make([]*ent.Message, 0, len(prevMsgs)+len(nextMsgs)+1)
	window = append(window, targetMsg)
	window = append(window, prevMsgs...)
	window = append(window, nextMsgs...)
	relations, err := r.hydrateMessageRelations(ctx, window...)
	if err != nil {
		return nil, err
	}
	if hydrated, ok := relations[targetMsg.ID]; ok {
		targetMsg = hydrated
	}
	for i, msg := range prevMsgs {
		if hydrated, ok := relations[msg.ID]; ok {
			prevMsgs[i] = hydrated
		}
	}
	for i, msg := range nextMsgs {
		if hydrated, ok := relations[msg.ID]; ok {
			nextMsgs[i] = hydrated
		}
	}

	result := make([]*ent.Message, 0, len(prevMsgs)+1+len(nextMsgs))

	for i := len(prevMsgs) - 1; i >= 0; i-- {
		result = append(result, prevMsgs[i])
	}

	result = append(result, targetMsg)
	result = append(result, nextMsgs...)

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID.String() < result[j].ID.String()
	})

	return &MessageAroundPage{
		Messages: result,
		HasOlder: hasOlder,
		HasNewer: hasNewer,
	}, nil
}
