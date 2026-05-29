package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

type stubSummarizer func(context.Context, []contexty.Message) (contexty.Message, error)

func (f stubSummarizer) Summarize(ctx context.Context, msgs []contexty.Message) (contexty.Message, error) {
	return f(ctx, msgs)
}
