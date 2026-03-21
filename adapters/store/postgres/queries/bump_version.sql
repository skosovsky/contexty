UPDATE {{.MetaTable}}
SET version = version + 1
WHERE thread_id = @thread_id AND version = @expected_version;
