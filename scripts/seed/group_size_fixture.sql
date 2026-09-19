DO $$
DECLARE
    alice_id uuid;
    fixture_chat_id uuid;
    group_id uuid;
    member_id uuid;
    message_id uuid;
    user_record record;
    group_number integer;
    member_limit integer;
    member_index integer;
BEGIN
    SELECT id INTO alice_id FROM users WHERE email = 'alice@atoitalk.local';
    IF alice_id IS NULL THEN
        RAISE EXCEPTION 'diagnostic users are missing';
    END IF;

    FOR group_number, member_limit IN VALUES (10, 10), (50, 50), (100, 100) LOOP
        fixture_chat_id := ('00000000-0000-0000-0000-' || lpad(group_number::text, 12, '0'))::uuid;
        group_id := ('00000000-0000-0000-0001-' || lpad(group_number::text, 12, '0'))::uuid;

        INSERT INTO chats (id, created_at, updated_at, type)
        VALUES (fixture_chat_id, now(), now(), 'group')
        ON CONFLICT (id) DO NOTHING;

        INSERT INTO group_chats (id, name, description, is_public, invite_code, chat_id, created_by)
        VALUES (
            group_id,
            format('Group Size %s', group_number),
            'Diagnostic group-size benchmark fixture',
            true,
            format('group-size-invite-%s', group_number),
            fixture_chat_id,
            alice_id
        )
        ON CONFLICT (id) DO NOTHING;

        member_index := 0;
        FOR user_record IN
            SELECT id
            FROM users
            WHERE deleted_at IS NULL
              AND is_banned = false
            ORDER BY created_at, id
            LIMIT member_limit
        LOOP
            member_index := member_index + 1;
            member_id := ('00000000-0000-0000-0011-' || lpad((group_number * 1000 + member_index)::text, 12, '0'))::uuid;
            INSERT INTO group_members (id, role, joined_at, unread_count, group_chat_id, user_id)
            VALUES (
                member_id,
                CASE WHEN user_record.id = alice_id THEN 'owner' ELSE 'member' END,
                now(),
                0,
                group_id,
                user_record.id
            )
            ON CONFLICT (group_chat_id, user_id) DO NOTHING;
        END LOOP;

        SELECT id INTO message_id
        FROM messages
        WHERE messages.chat_id = fixture_chat_id
        ORDER BY created_at DESC, id DESC
        LIMIT 1;

        IF message_id IS NULL THEN
            message_id := ('00000000-0000-0000-0021-' || lpad(group_number::text, 12, '0'))::uuid;
            INSERT INTO messages (id, created_at, updated_at, type, content, chat_id, sender_id)
            VALUES (message_id, now(), now(), 'regular', 'Diagnostic group-size message', fixture_chat_id, alice_id);
        END IF;

        UPDATE chats
        SET last_message_id = message_id, last_message_at = now(), updated_at = now()
        WHERE id = fixture_chat_id;
    END LOOP;
END $$;

SELECT gc.name, c.id, count(gm.id) AS members
FROM group_chats gc
JOIN chats c ON c.id = gc.chat_id
JOIN group_members gm ON gm.group_chat_id = gc.id
WHERE gc.name LIKE 'Group Size %'
GROUP BY gc.name, c.id
ORDER BY gc.name;
