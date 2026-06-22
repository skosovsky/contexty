package contexty_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureRoleLabelPolicy(t *testing.T, scenario string, calls *int) fixtureLabelPolicy {
	t.Helper()
	return func(_ context.Context, inputs []contexty.Message, output contexty.Message,
		descriptor contexty.Descriptor) (contexty.LabelDecision, error) {
		decision := contexty.LabelDecision{Extensions: inputs[0].Extensions}
		if descriptor.ID != "role" {
			return decision, nil
		}
		*calls++
		require.Equal(t, contexty.RoleUser, inputs[0].Role)
		require.Equal(t, contexty.RoleSystem, output.Role)
		if scenario == "conflict" {
			return contexty.LabelDecision{}, fmt.Errorf("host rejected transition: %w", contexty.ErrLabelConflict)
		}
		if scenario == "upgrade" || scenario == "missing-decision" {
			decision.Upgrade = true
			decision.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"host":"approved"}`}}
			if scenario == "upgrade" {
				decision.DecisionRef = "host-authorization"
			}
		}
		return decision, nil
	}
}
