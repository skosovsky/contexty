package contexty

import (
	"errors"
	"testing"
)

func TestBudgetDecisionRejectsImpossibleSummary(t *testing.T) {
	for _, capacity := range []SummaryBudget{
		{MaxTokens: 11, TargetTokens: 5, Purpose: Descriptor{ID: "host", Revision: "1"}},
		{MaxTokens: 10, TargetTokens: 6, Purpose: Descriptor{ID: "host", Revision: "1"}},
	} {
		// Arrange: summary evidence contradicts the enclosing capacity or soft target.
		decision := BudgetDecision{HardLimit: 10, TriggerTokens: 8, TargetTokens: 5,
			BeforeTokens: 9, AfterTokens: 4, Required: nil, Summary: &capacity, Compacted: true, TargetReached: true}
		// Act.
		err := decision.validate()
		// Assert.
		if !errors.Is(err, ErrInvalidBudgetRequest) {
			t.Fatalf("expected invalid summary capacity, got %v", err)
		}
	}
}
