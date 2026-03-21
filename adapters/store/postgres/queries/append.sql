INSERT INTO {{.Table}} (thread_id, message_data)
SELECT @thread_id, payload::jsonb
FROM unnest(@payloads::text[]) AS payload;
