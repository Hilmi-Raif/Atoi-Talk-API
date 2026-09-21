UPDATE chats
SET last_message_id = NULL,
    last_message_at = NULL;

DELETE FROM message_outboxes;
ALTER TABLE messages DISABLE TRIGGER ALL;
DELETE FROM messages;
ALTER TABLE messages ENABLE TRIGGER ALL;
DELETE FROM media
WHERE message_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM users WHERE users.avatar_id = media.id)
  AND NOT EXISTS (SELECT 1 FROM group_chats WHERE group_chats.avatar_id = media.id);

UPDATE private_chats
SET user1_last_read_at = NULL,
    user2_last_read_at = NULL,
    user1_hidden_at = NULL,
    user2_hidden_at = NULL,
    user1_unread_count = 0,
    user2_unread_count = 0;

UPDATE group_members
SET last_read_at = NULL,
    unread_count = 0;
