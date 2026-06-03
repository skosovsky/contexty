package contexty

// SegmentFormatter projects a segment's messages before token budgeting (host-defined).
type SegmentFormatter func(messages []Message) []Message
