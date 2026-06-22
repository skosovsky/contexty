package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type fixtureLabelPolicy func(context.Context, []contexty.Message, contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error)

func (f fixtureLabelPolicy) ProjectLabels(ctx context.Context, inputs []contexty.Message,
	output contexty.Message, descriptor contexty.Descriptor,
) (contexty.LabelDecision, error) {
	return f(ctx, inputs, output, descriptor)
}

type fixtureWireExtension struct {
	wire string
}

func (e fixtureWireExtension) ExtensionType() string { return "fixture-label" }

func (e fixtureWireExtension) CloneExtension() contexty.Extension { return e }

func (e fixtureWireExtension) MarshalJSON() ([]byte, error) { return []byte(e.wire), nil }

func fixtureRef(t *testing.T, id, text string) contexty.ContentRef {
	t.Helper()
	msg := contexty.TextMessage(contexty.RoleUser, text)
	msg.ID = id
	ref, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	return ref
}
