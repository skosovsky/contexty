package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

type fixtureDeferredHook func(context.Context, contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error)

func (f fixtureDeferredHook) Transform(
	ctx context.Context,
	snapshot contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	return f(ctx, snapshot)
}
