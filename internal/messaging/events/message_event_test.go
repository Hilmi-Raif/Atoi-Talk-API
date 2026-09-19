package events

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMessageEventRoundTripsWithStableIdentifiers(t *testing.T) {
	event := MessageEvent{
		ID:           uuid.New(),
		Type:         "message.new",
		MessageID:    uuid.New(),
		ChatID:       uuid.New(),
		SenderID:     uuid.New(),
		TargetUserID: uuid.New(),
		Payload:      []byte(`{"type":"message.new"}`),
	}

	encoded, err := event.Marshal()
	require.NoError(t, err)

	decoded, err := UnmarshalMessageEvent(encoded)
	require.NoError(t, err)
	require.Equal(t, event, decoded)
}

func TestMessageEventDoesNotRequireContextForEncoding(t *testing.T) {
	event := MessageEvent{ID: uuid.New(), Type: "message.delete"}
	_, err := event.MarshalContext(context.Background())
	require.NoError(t, err)
}
