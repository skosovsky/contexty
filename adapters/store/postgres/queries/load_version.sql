SELECT version
FROM {{.MetaTable}}
WHERE thread_id = @thread_id;
