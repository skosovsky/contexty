package contexty

import "context"

// SegmentFormatter projects a segment's messages before token budgeting (host-defined).
type SegmentFormatter func(ctx context.Context, messages []Message) ([]Message, error)
