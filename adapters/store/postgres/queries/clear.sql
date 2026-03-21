DELETE FROM {{.Table}}
WHERE thread_id = @thread_id;
