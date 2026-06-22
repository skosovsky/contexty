package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

type fixturePrefixExtension struct {
	encodes *int
	cancel  context.CancelFunc
}

func (e fixturePrefixExtension) ExtensionType() string { return "prefix-test" }

func (e fixturePrefixExtension) CloneExtension() contexty.Extension { return e }

func (e fixturePrefixExtension) MarshalJSON() ([]byte, error) {
	*e.encodes++
	if e.cancel != nil {
		e.cancel()
		return nil, contexty.ErrInvalidDescriptor
	}
	return []byte(`{"host":"opaque"}`), nil
}

func fixturePrefixMessages() []contexty.Message {
	result := make([]contexty.Message, 0, 3)
	for _, id := range []string{"policy", "reference", "tail"} {
		msg := contexty.TextMessage(contexty.RoleSystem, id)
		msg.ID = id
		result = append(result, msg)
	}
	return result
}

func fixturePrefixRecipe() contexty.PrefixRecipe {
	return contexty.PrefixRecipe{
		Renderer: contexty.Descriptor{ID: "renderer", Revision: "pinned"},
		Encoding: contexty.Descriptor{ID: "semantic-json", Revision: "pinned"},
		Policy:   contexty.Descriptor{ID: "host-admission", Revision: "pinned"},
		Codec:    contexty.DefaultJSONSerializer(),
		Boundaries: []contexty.PrefixBoundary{
			{ID: "static-policy", AfterMessageID: "policy"},
			{ID: "static-reference", AfterMessageID: "reference"},
		},
		Authorize: func(context.Context, contexty.Message) error { return nil },
	}
}
