package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestContract_Immutability(t *testing.T) {
	for _, channel := range []string{"request", "main", "source", "first", "artifacts", "manifest", "record", "accepted", "snapshot"} {
		t.Run(channel, func(t *testing.T) {
			// Arrange: one actual compile has main, two targets and an accepted record.
			request, result, accepted := fixtureOwnedCompile(t)
			baseline := fixtureOwnedChannels(t, request, result, accepted)
			expected, err := contexty.ReplayExpectationFor(*result.Manifest)
			require.NoError(t, err)
			wire, err := contexty.EncodeSavedRecord(accepted)
			require.NoError(t, err)
			// Act: deliberately mutate one returned/host-owned channel.
			fixtureMutateChannel(channel, &request, &result, &accepted)
			actual := fixtureOwnedChannels(t, request, result, accepted)
			// Assert: no other channel changes, including nested provenance and saved bytes.
			for name, before := range baseline {
				if name != channel {
					require.Equal(t, before, actual[name], "mutation=%s, affected=%s", channel, name)
				}
			}
			restored, err := contexty.DecodeSavedRecord(wire)
			require.NoError(t, err)
			replayed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
			require.NoError(t, err)
			require.Equal(t, "safe", replayed.Outputs[0].Segments[string(contexty.SegmentHistory)][0].TextContent())
			replayed.Outputs[0].Segments[string(contexty.SegmentHistory)][0].Parts[0] = contexty.TextPart{
				Text: "mutated",
			}
			repeated, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
			require.NoError(t, err)
			require.Equal(t, "safe", repeated.Outputs[0].Segments[string(contexty.SegmentHistory)][0].TextContent())
		})
	}
}
