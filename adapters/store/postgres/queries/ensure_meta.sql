INSERT INTO {{.MetaTable}} (thread_id, version)
VALUES (@thread_id, 0)
ON CONFLICT (thread_id) DO NOTHING;
