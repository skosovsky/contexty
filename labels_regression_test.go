package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/skosovsky/contexty"
)

func TestLabel_CancellationDominance(t *testing.T) {
	// Arrange: the standalone host label policy cancels and fails together.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostErr := errors.New("host label failed")
	projection := contexty.LabelProjection{
		Policy: fixtureLabelPolicy(
			func(context.Context, []contexty.Message, contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error) {
				cancel()
				return contexty.LabelDecision{}, hostErr
			},
		),
	}
	message := contexty.TextMessage(contexty.RoleUser, "input")
	// Act.
	result, decision, err := projection.Project(
		ctx,
		[]contexty.Message{message},
		message,
		contexty.Descriptor{ID: "labels", Revision: "1"},
	)
	// Assert: the documented cancellation contract dominates simultaneous errors.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v (host failure preserved: %t)", err, errors.Is(err, hostErr))
	}
	if result.ID != "" || len(result.Parts) != 0 || decision != "" {
		t.Fatal("canceled projection returned partial content")
	}
}
