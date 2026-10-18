package pushworker

import (
	"context"
	"strings"
	"unicode"

	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/groupchat"
)

func previewText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}

func notificationPreview(ctx context.Context, client *ent.Client, msg *ent.Message) (string, string, error) {
	name := "AtoiTalk"
	if msg.SenderID != nil {
		sender, err := client.User.Get(ctx, *msg.SenderID)
		if err != nil {
			return "", "", err
		}
		if sender.FullName != nil {
			if clean := previewText(*sender.FullName, 80); clean != "" {
				name = clean
			}
		}
	}
	body := ""
	if msg.Content != nil {
		body = previewText(*msg.Content, 160)
	}
	if body == "" {
		body = "Mengirim lampiran"
	}
	group, err := client.GroupChat.Query().Where(groupchat.ChatID(msg.ChatID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return "", "", err
	}
	if group != nil {
		return previewText(group.Name, 80), previewText(name+": "+body, 160), nil
	}
	return name, body, nil
}
