package contexty_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/skosovsky/contexty"
)

func BenchmarkRemediation_Append(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			// Arrange: an immutable history and one caller-owned append.
			messages := remediationBenchMessages(size)
			state := contexty.EmptyState().WithSegment(contexty.SegmentHistory, messages)
			delta := contexty.ConversationDelta{
				Operation: contexty.DeltaAppendMessages,
				Segment:   contexty.SegmentHistory,
				Messages:  messages[:1],
			}
			b.ReportAllocs()
			b.ResetTimer()
			// Act / Assert: benchmark the public ownership boundary.
			for range b.N {
				if _, err := contexty.ApplyDelta(state, delta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRemediation_CompileSnapshots(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		for _, targets := range []int{0, 1, 4} {
			b.Run(fmt.Sprintf("history%d/targets%d", size, targets), func(b *testing.B) {
				// Arrange: explicit identities and estimator, no host callback cache.
				engine := contexty.NewEngine(
					contexty.WithBudgetPipeline(
						contexty.NewBudgetPipeline(
							contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1000000)},
							&contexty.FixedEstimator{TokensPerMessage: 1, TokensPerContentPart: 1},
						),
					),
				)
				request := contexty.CompileRequest{History: remediationBenchMessages(size)}
				for index := range targets {
					request.Targets = append(
						request.Targets,
						contexty.CompileTarget{
							Name:     strconv.Itoa(index),
							Segments: []contexty.SegmentName{contexty.SegmentHistory},
						},
					)
				}
				b.ReportAllocs()
				b.ResetTimer()
				// Act / Assert: preserve source/prepared/output policy boundaries.
				for range b.N {
					if _, err := engine.CompileSnapshot(context.Background(), request); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func remediationBenchMessages(size int) []contexty.Message {
	messages := make([]contexty.Message, size)
	for index := range messages {
		messages[index] = contexty.TextMessage(contexty.RoleUser, "owned benchmark content")
		messages[index].ID = strconv.Itoa(index)
	}
	return messages
}

func BenchmarkRemediation_EvidenceComponents(b *testing.B) {
	for _, size := range []int{10, 100} {
		// Arrange: the same owned text input for each isolated evidence operation.
		messages := remediationBenchMessages(size)
		codec := contexty.DefaultJSONSerializer()
		estimator := contexty.CharTokenEstimator{}
		reporter, err := contexty.NewEstimateReporter(estimator, fixtureEstimateProfile(), codec)
		if err != nil {
			b.Fatal(err)
		}
		request := contexty.EstimateRequest{
			Budget:   contexty.EffectiveInputBudget(1000000),
			Segments: []contexty.EstimateSegment{{Name: "history", Messages: messages}},
		}
		for _, component := range []string{"digest", "estimate", "report", "codec-roundtrip"} {
			b.Run(fmt.Sprintf("%s/%d", component, size), func(b *testing.B) {
				b.ReportAllocs()
				// Act / Assert: do the actual operation each time, without memoization.
				for range b.N {
					if err := remediationEvidenceComponent(
						component,
						messages,
						codec,
						estimator,
						reporter,
						request,
					); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkRemediation_LabelRoundtrip(b *testing.B) {
	// Arrange: a real required host label transported through its strict codec boundary.
	registry := contexty.NewExtensionRegistry()
	registry.Register(
		"fixture-label",
		func(data []byte) (contexty.Extension, error) { return fixtureWireExtension{wire: string(data)}, nil },
	)
	message := contexty.TextMessage(contexty.RoleUser, "labelled")
	message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"host":"label"}`}}
	projection := contexty.LabelProjection{
		Registry:      registry,
		RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(
			func(_ context.Context, _ []contexty.Message, output contexty.Message, _ contexty.Descriptor) (contexty.LabelDecision, error) {
				return contexty.LabelDecision{Extensions: output.Extensions}, nil
			},
		),
	}
	b.ReportAllocs()
	// Act / Assert: repeated input/output roundtrips are kept and counted.
	for range b.N {
		if _, _, err := projection.Project(
			context.Background(),
			[]contexty.Message{message},
			message,
			contexty.Descriptor{ID: "host", Revision: "v1"},
		); err != nil {
			b.Fatal(err)
		}
	}
}

func remediationEvidenceComponent(
	component string,
	messages []contexty.Message,
	codec contexty.JSONSerializer,
	estimator contexty.CharTokenEstimator,
	reporter *contexty.EstimateReporter,
	request contexty.EstimateRequest,
) error {
	switch component {
	case "digest":
		return remediationDigestLoop(messages, codec)
	case "estimate":
		_, err := estimator.Estimate(context.Background(), messages)
		return err
	case "report":
		_, err := reporter.Report(context.Background(), request)
		return err
	case "codec-roundtrip":
		return remediationCodecLoop(messages, codec)
	default:
		return fmt.Errorf("unknown component %s", component)
	}
}

func remediationDigestLoop(messages []contexty.Message, codec contexty.JSONSerializer) error {
	for _, message := range messages {
		if _, err := contexty.MessageContentRef(message, codec); err != nil {
			return err
		}
	}
	return nil
}

func remediationCodecLoop(messages []contexty.Message, codec contexty.JSONSerializer) error {
	for _, message := range messages {
		wire, err := codec.Marshal(message)
		if err != nil {
			return err
		}
		var restored contexty.Message
		if err = codec.Unmarshal(wire, &restored); err != nil {
			return err
		}
	}
	return nil
}
