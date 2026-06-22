package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestEstimate_Report(t *testing.T) {
	// Arrange: pinned approximate counter and ordered segments.
	a := contexty.TextMessage(contexty.RoleSystem, "abc")
	a.ID = "a"
	b := contexty.TextMessage(contexty.RoleUser, "12345")
	b.ID = "b"
	profile := fixtureEstimateProfile()
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	request := contexty.EstimateRequest{Budget: contexty.WindowInputBudget(10, 3, 1),
		Segments: []contexty.EstimateSegment{{Name: "system", Messages: []contexty.Message{a}},
			{Name: "history", Messages: []contexty.Message{b}}, {Name: "memory"}}}
	// Act.
	report, err := reporter.Report(context.Background(), request)
	// Assert: exact identities, explicit approximate quality, coherent totals and overflow.
	require.NoError(t, err)
	require.Equal(t, 8, report.Total)
	require.Equal(t, 6, report.EffectiveLimit)
	require.Equal(t, contexty.EstimateEstimated, report.Quality)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, report.OverflowReason)
	require.Equal(t, []int{3}, report.Segments[0].PerMessage)
	require.Equal(t, []int{5}, report.Segments[1].PerMessage)
	require.Zero(t, report.Segments[2].Tokens)
	require.Equal(t, fixtureRefForMessage(t, b), report.Segments[1].Messages[0])
	again, err := reporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, report, again)
	profile.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	again, err = reporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, report.ProfileDigest, again.ProfileDigest)
	request.Segments[1].Messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed"}}
	changed, err := reporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.NotEqual(t, report.RequestDigest, changed.RequestDigest)
	profile = fixtureEstimateProfile()
	profile.Model.Revision = "new"
	newReporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	changed, err = newReporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.NotEqual(t, report.ProfileDigest, changed.ProfileDigest)
	// Arrange / Act / Assert: legacy approximate counters cannot claim counted evidence.
	profile.Capabilities[contexty.EstimateText] = contexty.EstimateCounted
	_, err = contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, profile, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
	profile = fixtureEstimateProfile()
	profile.Capabilities[contexty.EstimateImage] = contexty.EstimateEstimated
	_, err = contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, profile, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
}

func TestEstimate_Coverage(t *testing.T) {
	// Arrange: unknown image and binary tool result after an otherwise supported segment.
	text := contexty.TextMessage(contexty.RoleUser, "abc")
	text.ID = "text"
	image := contexty.TextMessage(contexty.RoleUser, "")
	image.ID, image.Parts = "image", []contexty.ContentPart{contexty.ImagePart{URL: "long-private-url", Detail: "high"}}
	binary := contexty.TextMessage(contexty.RoleTool, "")
	binary.ID = "binary"
	binary.Parts = []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "call",
		Payload: contexty.BinaryPayload([]byte{1, 2, 3}, "audio/test")}}
	calls := 0
	estimator := fixtureEvidenceEstimator{
		per: func(ctx context.Context, messages []contexty.Message) ([]int, error) {
			calls++
			return contexty.CharTokenEstimator{}.EstimatePerMessage(ctx, messages)
		},
		total: func(ctx context.Context, messages []contexty.Message) (int, error) {
			calls++
			return contexty.CharTokenEstimator{}.Estimate(ctx, messages)
		}}
	profile := fixtureEstimateProfile()
	reporter, err := contexty.NewEstimateReporter(estimator, profile, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(100),
		Segments: []contexty.EstimateSegment{{Name: "text", Messages: []contexty.Message{text}},
			{Name: "media", Messages: []contexty.Message{image, binary}}}}
	// Act / Assert: strict missing cost does not run any estimator callback.
	report, err := reporter.Report(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	require.Zero(t, report)
	require.Zero(t, calls)
	// Arrange / Act / Assert: fallback is explicit and charged once per unknown part.
	profile.Fallback = &contexty.EstimateFallback{
		Policy: contexty.Descriptor{ID: "fallback", Revision: "pinned"},
		Tokens: 11,
	}
	reporter, err = contexty.NewEstimateReporter(estimator, profile, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	report, err = reporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, contexty.EstimateUnknown, report.Quality)
	require.Equal(t, 25, report.Total)
	require.Equal(t, []int{11, 11}, report.Segments[1].PerMessage)
	require.Len(t, report.Segments[1].Coverage, 3)
	require.Equal(t, image, request.Segments[1].Messages[0])
	require.Equal(t, binary, request.Segments[1].Messages[1])
}

func TestEstimate_Failures(t *testing.T) {
	// Arrange: deliberately broken optional implementations.
	message := contexty.TextMessage(contexty.RoleUser, "x")
	message.ID = "m"
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(10),
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}}
	for _, weights := range [][]int{{-1}, {1, 2}, {2}} {
		estimator := fixtureEvidenceEstimator{per: func(context.Context, []contexty.Message) ([]int, error) {
			return weights, nil
		}, total: func(context.Context, []contexty.Message) (int, error) { return 1, nil }}
		reporter, err := contexty.NewEstimateReporter(
			estimator,
			fixtureEstimateProfile(),
			contexty.DefaultJSONSerializer(),
		)
		require.NoError(t, err)
		// Act / Assert: malformed evidence never produces a partial report.
		report, err := reporter.Report(context.Background(), request)
		require.ErrorIs(t, err, contexty.ErrInconsistentEstimate)
		require.Zero(t, report)
	}
	failure := errors.New("estimator failed")
	estimator := fixtureEvidenceEstimator{per: func(context.Context, []contexty.Message) ([]int, error) {
		return nil, failure
	}, total: func(context.Context, []contexty.Message) (int, error) { return 0, nil }}
	reporter, err := contexty.NewEstimateReporter(estimator, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	_, err = reporter.Report(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrTokenCountFailed)
	require.ErrorIs(t, err, failure)
	ctx, cancel := context.WithCancel(context.Background())
	estimator.per = func(context.Context, []contexty.Message) ([]int, error) { cancel(); return []int{1}, nil }
	reporter, err = contexty.NewEstimateReporter(estimator, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	report, err := reporter.Report(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, report)
}

func TestEstimate_MediaIdentity(t *testing.T) {
	// Arrange: caller-owned estimator distinguishes image properties, not URL length.
	weight := func(ctx context.Context, messages []contexty.Message) ([]int, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		weights := make([]int, len(messages))
		for i, message := range messages {
			for _, part := range message.Parts {
				image, ok := part.(contexty.ImagePart)
				if ok {
					weights[i]++
					if image.Detail == "high" {
						weights[i] += 19
					}
				}
			}
		}
		return weights, nil
	}
	estimator := fixtureEvidenceEstimator{per: weight,
		total: func(ctx context.Context, messages []contexty.Message) (int, error) {
			weights, err := weight(ctx, messages)
			var total int
			for _, value := range weights {
				total += value
			}
			return total, err
		}}
	profile := fixtureEstimateProfile()
	profile.Capabilities[contexty.EstimateImage] = contexty.EstimateEstimated
	reporter, err := contexty.NewEstimateReporter(estimator, profile, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	image := contexty.TextMessage(contexty.RoleUser, "")
	image.ID = "image"
	image.Parts = []contexty.ContentPart{contexty.ImagePart{URL: "same-url", Detail: "low"}}
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(10),
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{image}}}}
	// Act.
	low, err := reporter.Report(context.Background(), request)
	require.NoError(t, err)
	request.Segments[0].Messages[0].Parts = []contexty.ContentPart{contexty.ImagePart{URL: "same-url", Detail: "high"}}
	high, err := reporter.Report(context.Background(), request)
	// Assert: media identity changes evidence and discovers overflow independently of URL length.
	require.NoError(t, err)
	require.Equal(t, 1, low.Total)
	require.Equal(t, 20, high.Total)
	require.Empty(t, low.OverflowReason)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, high.OverflowReason)
	require.NotEqual(t, low.RequestDigest, high.RequestDigest)
	require.Equal(t, contexty.EstimateEstimated, high.Quality)
}

func TestEstimate_WholeRequest(t *testing.T) {
	// Arrange: one shared envelope overhead, attributed to the first message.
	a := contexty.TextMessage(contexty.RoleSystem, "a")
	a.ID = "a"
	b := contexty.TextMessage(contexty.RoleUser, "b")
	b.ID = "b"
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(10),
		Segments: []contexty.EstimateSegment{{Name: "system", Messages: []contexty.Message{a}},
			{Name: "history", Messages: []contexty.Message{b}}}}
	calls := 0
	estimator := fixtureEvidenceEstimator{
		per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
			calls++
			require.Len(t, messages, 2)
			request.Segments[1].Messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "outside mutation"}}
			messages[1].Parts = nil
			return []int{7, 0}, nil
		}, total: func(_ context.Context, messages []contexty.Message) (int, error) {
			calls++
			require.Len(t, messages, 2)
			require.Equal(t, "b", messages[1].TextContent())
			return 7, nil
		}}
	reporter, err := contexty.NewEstimateReporter(estimator, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	// Act.
	report, err := reporter.Report(context.Background(), request)
	// Assert: shared overhead is not repeated, and callback changes cannot alter evidence.
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, 7, report.Total)
	require.Equal(t, 7, report.Segments[0].Tokens)
	require.Zero(t, report.Segments[1].Tokens)
	require.Equal(t, fixtureRefForMessage(t, b), report.Segments[1].Messages[0])
}

func TestEstimate_CallbackWeights(t *testing.T) {
	// Arrange: the total callback mutates the prior callback's returned backing slice.
	weights := []int{1}
	estimator := fixtureEvidenceEstimator{
		per:   func(context.Context, []contexty.Message) ([]int, error) { return weights, nil },
		total: func(context.Context, []contexty.Message) (int, error) { weights[0] = 2; return 2, nil },
	}
	reporter, err := contexty.NewEstimateReporter(estimator, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	message := contexty.TextMessage(contexty.RoleUser, "x")
	message.ID = "m"
	// Act / Assert: freeze the returned per-message evidence before another callback runs.
	report, err := reporter.Report(
		context.Background(),
		contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(10),
			Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}},
	)
	require.ErrorIs(t, err, contexty.ErrInconsistentEstimate)
	require.Zero(t, report)
}
