SELECT message_data
FROM {{.Table}}
WHERE thread_id = @thread_id
ORDER BY id ASC;
