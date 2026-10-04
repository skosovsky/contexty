package evaluation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBehaviorChecksUseRealContracts(t *testing.T) {
	for _, kind := range []string{"partial-round", "resource-access", "blob-access", "consumers", "opaque-invalidation"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			messages := make([]contexty.Message, 3)
			for i := range messages {
				messages[i] = contexty.TextMessage(contexty.RoleUser, "ordinary input")
				messages[i].ID = string(rune('a' + i))
			}
			fixture := Fixture{
				CaseKind: kind,
				Messages: messages,
			}
			// Act.
			checks, err := behaviorChecks(t.Context(), fixture, 640)
			// Assert.
			require.NoError(t, err)
			require.NotEmpty(t, checks)
			for _, check := range checks {
				require.True(t, check.Passed, "%s: %s", check.Kind, check.Detail)
			}
		})
	}
}

func TestBehaviorChecksRejectInsufficientConsumerFixture(t *testing.T) {
	// Arrange.
	fixture := Fixture{
		CaseKind: "consumers",
		Messages: nil,
	}
	// Act.
	checks, err := behaviorChecks(t.Context(), fixture, 640)
	// Assert.
	require.Error(t, err)
	require.Nil(t, checks)
}
