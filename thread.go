package contexty

import (
	"context"
	"errors"
	"fmt"
)

const maxThreadRetries = 32

// Thread coordinates a history store with a builder history block.
type Thread struct {
	store        HistoryStore
	threadID     string
	historyBlock string
}

// NewThread returns a thread orchestrator for the given store, thread ID, and history block name.
func NewThread(store HistoryStore, threadID string, historyBlock string) (*Thread, error) {
	if store == nil || threadID == "" || historyBlock == "" {
		return nil, ErrInvalidThreadConfig
	}
	return &Thread{
		store:        store,
		threadID:     threadID,
		historyBlock: historyBlock,
	}, nil
}

// BuildContext loads stored history, injects it into base, builds the final prompt,
// and synchronizes any history compaction back to the store using optimistic concurrency.
func (t *Thread) BuildContext(ctx context.Context, base *Builder, newMsgs []Message) ([]Message, error) {
	if t == nil || t.store == nil || t.threadID == "" || t.historyBlock == "" || base == nil {
		return nil, ErrInvalidThreadConfig
	}

	for range maxThreadRetries {
		snap, err := t.store.Load(ctx, t.threadID)
		if err != nil {
			return nil, fmt.Errorf("contexty: thread load: %w", err)
		}

		history := snap.Messages
		candidateHistory := cloneMessages(history)
		candidateHistory = append(candidateHistory, cloneMessages(newMsgs)...)

		builder := base.Clone()
		if setErr := builder.SetBlockMessages(t.historyBlock, candidateHistory); setErr != nil {
			return nil, fmt.Errorf("contexty: thread set history block: %w", setErr)
		}

		result, err := builder.BuildDetailed(ctx)
		if err != nil {
			return nil, err
		}

		compactedHistory := findBlockMessages(result.Blocks, t.historyBlock)
		var writeErr error
		switch {
		case historiesEqual(candidateHistory, compactedHistory):
			if len(newMsgs) > 0 {
				writeErr = t.store.Append(ctx, t.threadID, snap.Version, cloneMessages(newMsgs)...)
			}
		case len(compactedHistory) == 0:
			writeErr = t.store.Save(ctx, t.threadID, snap.Version, nil)
		default:
			writeErr = t.store.Save(ctx, t.threadID, snap.Version, compactedHistory)
		}

		if writeErr == nil {
			return result.Messages, nil
		}
		if errors.Is(writeErr, ErrHistoryVersionConflict) {
			continue
		}
		return nil, fmt.Errorf("contexty: thread persist: %w", writeErr)
	}

	return nil, fmt.Errorf("contexty: thread: exceeded %d retries: %w", maxThreadRetries, ErrHistoryVersionConflict)
}

func findBlockMessages(blocks []NamedBlock, name string) []Message {
	for _, block := range blocks {
		if block.Name == name {
			return cloneMessages(block.Block.Messages)
		}
	}
	return nil
}
