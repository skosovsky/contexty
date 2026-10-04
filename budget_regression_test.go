package contexty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/contexty"
)

func TestBudgetPipeline_ObserverCancellation(t *testing.T) {
	for _, summary := range []bool{false, true} {
		t.Run(map[bool]string{false: "estimate", true: "summary"}[summary], func(t *testing.T) {
			// Arrange: telemetry callback cancels the caller operation.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := contexty.TextMessage(contexty.RoleUser, "ok")
			input.ID = "input"
			config := contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)}
			if summary {
				input.Parts = []contexty.ContentPart{contexty.TextPart{Text: strings.Repeat("x", 20)}}
				config.Summarizer = stubSummarizer(
					func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
						result := contexty.TextMessage(contexty.RoleAssistant, "ok")
						result.ID = "summary"
						return result, nil
					},
				)
			}
			pipe := contexty.NewBudgetPipeline(
				config,
				contexty.CharTokenEstimator{},
				contexty.WithBudgetObserver(budgetCallbackObserver{cancel: cancel, summary: summary}),
			)
			// Act.
			_, err := pipe.Apply(ctx, []contexty.Message{input})
			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled after observer, got %v", err)
			}
		})
	}
}

func TestBudgetPipeline_EvictionFinalBudget(t *testing.T) {
	// Arrange: a host strategy erroneously expands the output.
	input := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 20))
	input.ID = "input"
	output := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 100))
	output.ID = "output"
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(10),
			TruncateStrategy: budgetCallbackEviction{output: []contexty.Message{output}},
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	gotBudget, err := pipe.Apply(context.Background(), []contexty.Message{input})
	got := gotBudget.Messages
	// Assert: the core must reject a final over-budget result.
	if !errors.Is(err, contexty.ErrBudgetExceeded) || len(got) != 0 {
		t.Fatalf("want budget rejection and no output, got %v / %v", got, err)
	}
}

func TestBudgetPipeline_EvictionCancellation(t *testing.T) {
	// Arrange: the callback cancels the operation while returning a valid result.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 20))
	input.ID = "input"
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(10),
			TruncateStrategy: budgetCallbackEviction{cancel: cancel},
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	_, err := pipe.Apply(ctx, []contexty.Message{input})
	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestBudgetPipeline_EvictionEffectiveLimit(t *testing.T) {
	// Arrange: the invocation has a smaller effective limit than pipeline config.
	input := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 20))
	input.ID = "input"
	output := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 9))
	output.ID = "output"
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(10),
			TruncateStrategy: budgetCallbackEviction{output: []contexty.Message{output}},
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	gotBudget, err := pipe.ApplyWithLimit(context.Background(), []contexty.Message{input}, 5)
	got := gotBudget.Messages
	// Assert: checking only the configured budget would incorrectly accept 9.
	if !errors.Is(err, contexty.ErrBudgetExceeded) || len(got) != 0 {
		t.Fatalf("want invocation limit rejection, got %v / %v", got, err)
	}
}

func TestBudgetPipeline_EvictionOwnership(t *testing.T) {
	// Arrange: the host owns the slice returned by its callback.
	input := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 20))
	input.ID = "input"
	output := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "ok")}
	output[0].ID = "output"
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(10),
			TruncateStrategy: budgetCallbackEviction{output: output},
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	gotBudget, err := pipe.Apply(context.Background(), []contexty.Message{input})
	got := gotBudget.Messages
	if err != nil {
		t.Fatal(err)
	}
	output[0].Parts[0] = contexty.TextPart{Text: "mutated outside operation"}
	// Assert.
	if got[0].Parts[0].(contexty.TextPart).Text != "ok" {
		t.Fatalf("returned output aliases callback storage: %q", got[0].Parts[0].(contexty.TextPart).Text)
	}
}

func TestBudgetPipeline_SummaryOwnership(t *testing.T) {
	// Arrange: the summarizer retains ownership of its returned parts.
	input := contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 20))
	input.ID = "input"
	summary := contexty.TextMessage(contexty.RoleAssistant, "ok")
	summary.ID = "summary"
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(10),
			Summarizer: stubSummarizer(
				func(context.Context, contexty.SummaryRequest) (contexty.Message, error) { return summary, nil },
			),
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	gotBudget, err := pipe.Apply(context.Background(), []contexty.Message{input})
	got := gotBudget.Messages
	if err != nil {
		t.Fatal(err)
	}
	summary.Parts[0] = contexty.TextPart{Text: "mutated outside operation"}
	// Assert.
	if got[0].Parts[0].(contexty.TextPart).Text != "ok" {
		t.Fatalf("returned summary aliases callback storage: %q", got[0].Parts[0].(contexty.TextPart).Text)
	}
}
