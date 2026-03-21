// Package postgres provides a PostgreSQL-backed HistoryStore for contexty.
//
// Expected schema:
//
//	CREATE TABLE contexty_messages (
//	    id BIGSERIAL PRIMARY KEY,
//	    thread_id VARCHAR(255) NOT NULL,
//	    message_data JSONB NOT NULL,
//	    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//	CREATE INDEX idx_contexty_thread ON contexty_messages(thread_id, id);
package postgres
