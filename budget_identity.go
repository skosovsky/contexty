package contexty

import "context"

type budgetIdentityKey struct{}

func withBudgetIdentitySegment(ctx context.Context, seg SegmentName) context.Context {
	if seg == "" {
		return ctx
	}
	return context.WithValue(ctx, budgetIdentityKey{}, seg)
}

func budgetIdentitySegmentFrom(ctx context.Context) SegmentName {
	if ctx == nil {
		return SegmentHistory
	}
	if seg, ok := ctx.Value(budgetIdentityKey{}).(SegmentName); ok && seg != "" {
		return seg
	}
	if blockID := budgetBlockIDFrom(ctx); blockID != "" {
		return SegmentName(blockID)
	}
	return SegmentHistory
}
