DO $$
DECLARE
    alice_id uuid;
    bob_id uuid;
    charlie_id uuid;
    fixture_chat_id uuid;
    group_id uuid;
    member_id uuid;
    message_id uuid;
    chat_number integer;
BEGIN
    SELECT id INTO alice_id FROM users WHERE email = 'alice@atoitalk.local';
    SELECT id INTO bob_id FROM users WHERE email = 'bob@atoitalk.local';
    SELECT id INTO charlie_id FROM users WHERE email = 'charlie@atoitalk.local';

    IF alice_id IS NULL OR bob_id IS NULL OR charlie_id IS NULL THEN
        RAISE EXCEPTION 'diagnostic users are missing';
    END IF;

    FOR chat_number IN 1..3 LOOP
        fixture_chat_id := ('00000000-0000-0000-0000-' || lpad(chat_number::text, 12, '0'))::uuid;
        group_id := ('00000000-0000-0000-0001-' || lpad(chat_number::text, 12, '0'))::uuid;

        INSERT INTO chats (id, created_at, updated_at, type)
        VALUES (fixture_chat_id, now(), now(), 'group')
        ON CONFLICT (id) DO NOTHING;

        INSERT INTO group_chats (id, name, description, is_public, invite_code, chat_id, created_by)
        VALUES (
            group_id,
            format('Distributed Load %s', chat_number),
            'Diagnostic multi-chat fixture',
            true,
            format('diagnostic-invite-%s', chat_number),
            fixture_chat_id,
            alice_id
        )
        ON CONFLICT (id) DO NOTHING;

        member_id := ('00000000-0000-0000-0010-' || lpad((chat_number * 10 + 1)::text, 12, '0'))::uuid;
        INSERT INTO group_members (id, role, joined_at, unread_count, group_chat_id, user_id)
        VALUES (member_id, 'owner', now(), 0, group_id, alice_id)
        ON CONFLICT (group_chat_id, user_id) DO NOTHING;

        member_id := ('00000000-0000-0000-0010-' || lpad((chat_number * 10 + 2)::text, 12, '0'))::uuid;
        INSERT INTO group_members (id, role, joined_at, unread_count, group_chat_id, user_id)
        VALUES (member_id, 'member', now(), 0, group_id, bob_id)
        ON CONFLICT (group_chat_id, user_id) DO NOTHING;

        member_id := ('00000000-0000-0000-0010-' || lpad((chat_number * 10 + 3)::text, 12, '0'))::uuid;
        INSERT INTO group_members (id, role, joined_at, unread_count, group_chat_id, user_id)
        VALUES (member_id, 'member', now(), 0, group_id, charlie_id)
        ON CONFLICT (group_chat_id, user_id) DO NOTHING;

        SELECT id INTO message_id
        FROM messages
        WHERE messages.chat_id = fixture_chat_id
        ORDER BY created_at DESC, id DESC
        LIMIT 1;

        IF message_id IS NULL THEN
            message_id := ('00000000-0000-0000-0020-' || lpad(chat_number::text, 12, '0'))::uuid;
            INSERT INTO messages (id, created_at, updated_at, type, content, chat_id, sender_id)
            VALUES (message_id, now(), now(), 'regular', 'Diagnostic fixture message', fixture_chat_id, alice_id);
        END IF;

        UPDATE chats
        SET last_message_id = message_id, last_message_at = now(), updated_at = now()
        WHERE id = fixture_chat_id;
    END LOOP;
END $$;

SELECT c.id
FROM chats c
JOIN group_chats gc ON gc.chat_id = c.id
WHERE gc.name LIKE 'Distributed Load %'
ORDER BY gc.name;
