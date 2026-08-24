package helper

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/message"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestToMessageResponseIncludesSenderAttachmentsAndReply(t *testing.T) {
	messageID := uuid.New()
	chatID := uuid.New()
	senderID := uuid.New()
	replyID := uuid.New()
	content := "hello"
	senderName := "Alice"
	replyContent := "previous"
	createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	attachmentID := uuid.New()

	got := ToMessageResponse(&ent.Message{
		ID:         messageID,
		ChatID:     chatID,
		SenderID:   &senderID,
		Type:       message.TypeRegular,
		Content:    &content,
		CreatedAt:  createdAt,
		ActionData: map[string]interface{}{"kind": "test"},
		Edges: ent.MessageEdges{
			Sender:      &ent.User{ID: senderID, FullName: &senderName, Edges: ent.UserEdges{Avatar: &ent.Media{FileName: "sender.png"}}},
			Attachments: []*ent.Media{{ID: attachmentID, FileName: "file.txt", OriginalName: "notes.txt", FileSize: 12, MimeType: "text/plain"}},
			ReplyTo:     &ent.Message{ID: replyID, Type: message.TypeRegular, Content: &replyContent, CreatedAt: createdAt, SenderID: &senderID, Edges: ent.MessageEdges{Sender: &ent.User{ID: senderID, FullName: &senderName}}},
		},
	}, testURLGenerator{}, nil, "member")

	if got.ID != messageID || got.ChatID != chatID || got.SenderID == nil || *got.SenderID != senderID || got.SenderName != senderName || got.SenderRole != "member" {
		t.Fatalf("unexpected message identity: %+v", got)
	}
	if got.Content != content || got.SenderAvatar != "https://cdn.test/sender.png" || len(got.Attachments) != 1 || got.Attachments[0].URL != "https://signed.test/file.txt" {
		t.Fatalf("unexpected message content: %+v", got)
	}
	if got.ReplyTo == nil || got.ReplyTo.ID != replyID || got.ReplyTo.Content != replyContent || got.ReplyTo.SenderID == nil {
		t.Fatalf("unexpected reply preview: %+v", got.ReplyTo)
	}
}

func TestToMessageResponseHandlesDeletedMessageAndSender(t *testing.T) {
	messageID := uuid.New()
	senderID := uuid.New()
	deletedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	got := ToMessageResponse(&ent.Message{
		ID:        messageID,
		SenderID:  &senderID,
		Type:      message.TypeRegular,
		CreatedAt: deletedAt,
		DeletedAt: &deletedAt,
		Content:   func() *string { value := "hidden"; return &value }(),
		Edges:     ent.MessageEdges{Sender: &ent.User{ID: senderID, DeletedAt: &deletedAt}},
	}, testURLGenerator{}, nil, "")

	if got.Content != "" || len(got.Attachments) != 0 || got.DeletedAt == nil || got.SenderName != "Deleted User" || got.SenderID != nil {
		t.Fatalf("unexpected deleted message response: %+v", got)
	}
}

func TestToMessageResponseNilMessage(t *testing.T) {
	if ToMessageResponse(nil, testURLGenerator{}, nil, "") != nil {
		t.Fatal("expected nil response for nil message")
	}
}

func TestToMessageResponseHandlesMissingOptionalEdges(t *testing.T) {
	content := "content"
	senderID := uuid.New()
	got := ToMessageResponse(&ent.Message{
		ID:        uuid.New(),
		ChatID:    uuid.New(),
		SenderID:  &senderID,
		Type:      message.TypeRegular,
		Content:   &content,
		CreatedAt: time.Now().UTC(),
	}, testURLGenerator{}, nil, "")
	if got == nil || got.Content != content || got.SenderID == nil || *got.SenderID != senderID {
		t.Fatalf("unexpected response without optional edges: %+v", got)
	}
}

func TestToMessageResponseMapsEditedActionAndReplyFallbacks(t *testing.T) {
	senderID := uuid.New()
	replyID := uuid.New()
	editedAt := time.Date(2026, time.January, 3, 3, 4, 5, 0, time.UTC)
	createdAt := editedAt.Add(-time.Hour)
	content := "updated"
	replyContent := "reply"

	got := ToMessageResponse(&ent.Message{
		ID:         uuid.New(),
		ChatID:     uuid.New(),
		SenderID:   &senderID,
		Type:       message.TypeSystemRename,
		Content:    &content,
		CreatedAt:  createdAt,
		EditedAt:   &editedAt,
		ActionData: map[string]interface{}{"action": "edit"},
		Edges: ent.MessageEdges{
			Sender: &ent.User{ID: senderID},
			ReplyTo: &ent.Message{
				ID:         replyID,
				SenderID:   &senderID,
				Type:       message.TypeRegular,
				Content:    &replyContent,
				CreatedAt:  createdAt,
				ActionData: map[string]interface{}{"kind": "reply"},
			},
		},
	}, testURLGenerator{}, nil, "admin")

	if got.EditedAt == nil || *got.EditedAt != editedAt.Format(time.RFC3339) || got.ActionData["action"] != "edit" {
		t.Fatalf("expected edited action data, got %+v", got)
	}
	if got.ReplyTo == nil || got.ReplyTo.SenderID == nil || *got.ReplyTo.SenderID != senderID || got.ReplyTo.ActionData["kind"] != "reply" {
		t.Fatalf("expected reply fallback/action data, got %+v", got.ReplyTo)
	}
}

func TestToMessageResponseMapsDeletedReplyAndDeletedSender(t *testing.T) {
	senderID := uuid.New()
	deletedAt := time.Date(2026, time.January, 3, 3, 4, 5, 0, time.UTC)

	got := ToMessageResponse(&ent.Message{
		ID:        uuid.New(),
		SenderID:  &senderID,
		Type:      message.TypeRegular,
		CreatedAt: deletedAt,
		Edges: ent.MessageEdges{
			Sender: &ent.User{ID: senderID, DeletedAt: &deletedAt},
			ReplyTo: &ent.Message{
				ID:        uuid.New(),
				Type:      message.TypeRegular,
				CreatedAt: deletedAt,
				DeletedAt: &deletedAt,
			},
		},
	}, testURLGenerator{}, nil, "")

	if got.SenderID != nil || got.SenderName != "Deleted User" || got.ReplyTo == nil || got.ReplyTo.DeletedAt == nil || got.ReplyTo.Content != "" {
		t.Fatalf("unexpected deleted sender/reply response: %+v", got)
	}
}
