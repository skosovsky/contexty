package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/skosovsky/contexty"
)

func TestCompile_CallbackCancellationDominance(t *testing.T) {
	for _, kind := range []string{"mapping", "privacy"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange: callback cancels context and returns its own error.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			profile := fixtureTraceProfile()
			options := []contexty.EngineOption{contexty.WithCompileRecording(fixtureRecordProfile())}
			if kind == "mapping" {
				options = nil
				profile.Mapping = func(context.Context, string, []contexty.Message, []contexty.Message) (map[string][]contexty.ContentRef, error) {
					cancel()
					return nil, errors.New("host callback error")
				}
			} else {
				options = append(
					options,
					contexty.WithCompileContentCapture(
						contexty.Descriptor{ID: "privacy", Revision: "pinned"},
						fixtureContentPolicy(func(context.Context, contexty.CaptureCandidate) (bool, error) {
							cancel()
							return false, errors.New("host callback error")
						}),
					),
				)
			}
			options = append(options, contexty.WithTraceProfile(profile))
			message := contexty.TextMessage(contexty.RoleUser, "input")
			message.ID = "input"
			// Act.
			_, err := fixtureEngine(options...).
				CompileSnapshot(ctx, contexty.CompileRequest{CompilationID: "audit-cancel", History: []contexty.Message{message}})
			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
		})
	}
}
