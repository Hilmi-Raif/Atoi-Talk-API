package outbox

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"go.opentelemetry.io/otel/trace"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestPersistMessageOutboxesCreatesOneRecordPerMessage(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-outbox?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}}))
	sender := client.User.Create().SetUsername("sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	first := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("first").SaveX(ctx)
	second := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("second").SaveX(ctx)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, PersistMessageOutboxes(ctx, tx, []*ent.Message{first, second}, events.EventMessageNew, sender.ID))
	require.NoError(t, tx.Commit())

	require.Equal(t, 2, client.MessageOutbox.Query().CountX(ctx))
	for _, row := range client.MessageOutbox.Query().AllX(ctx) {
		require.Equal(t, observability.TraceParent(ctx), row.Payload[observability.TraceParentKey])
	}
}

func TestPersistMessageOutboxesStopsOnInvalidMessage(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-outbox-invalid?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	err = PersistMessageOutbox(ctx, tx, &ent.Message{ID: uuid.New(), ChatID: uuid.New()}, events.EventMessageNew, uuid.New())
	require.Error(t, err)
}
