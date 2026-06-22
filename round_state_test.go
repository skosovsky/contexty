package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRound_States(t *testing.T) {
	// Arrange: missing result is pending, regardless of payload/control metadata.
	messages := fixtureRoundMessages()
	baseline := []contexty.Message{messages[0].Clone(), messages[1].Clone()}
	// Act.
	observations, err := contexty.InspectToolRoundStates(messages, nil)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, []contexty.ToolRoundObservation{{AssistantID: "assistant", Start: 0, End: 1,
		State: contexty.ToolRoundPending, MissingCallIDs: []string{"second"}}}, observations)
	require.Equal(t, baseline, messages)
	interrupted, err := contexty.InspectToolRoundStates(
		messages,
		map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted},
	)
	require.NoError(t, err)
	require.Equal(t, contexty.ToolRoundInterrupted, interrupted[0].State)
	require.Equal(t, baseline, messages)
	messages[1].Parts = append(
		messages[1].Parts,
		contexty.ToolResultPart{ToolCallID: "second", Payload: contexty.TextPayload("done")},
	)
	complete, err := contexty.InspectToolRoundStates(messages, nil)
	require.NoError(t, err)
	require.Equal(t, contexty.ToolRoundComplete, complete[0].State)
	require.Empty(t, complete[0].MissingCallIDs)
	_, err = contexty.InspectToolRoundStates(
		messages,
		map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted},
	)
	require.ErrorIs(t, err, contexty.ErrToolRoundStateConflict)
}

func TestRound_StateErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mutate       func([]contexty.Message) []contexty.Message
		declarations map[string]contexty.ToolRoundState
		want         error
	}{
		{name: "orphan", mutate: func(m []contexty.Message) []contexty.Message { return m[1:] }, want: contexty.ErrInvalidToolRound},
		{name: "duplicate result", mutate: func(m []contexty.Message) []contexty.Message { return append(m, m[1].Clone()) }, want: contexty.ErrInvalidToolRound},
		{name: "duplicate call", mutate: func(m []contexty.Message) []contexty.Message {
			m[0].Parts = append(m[0].Parts, m[0].Parts[0])
			return m
		}, want: contexty.ErrInvalidToolRound},
		{name: "wrong role", mutate: func(m []contexty.Message) []contexty.Message { m[0].Role = contexty.RoleUser; return m }, want: contexty.ErrInvalidToolRound},
		{name: "unknown result", mutate: func(m []contexty.Message) []contexty.Message {
			m[1].Parts = []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "unknown"}}
			return m
		}, want: contexty.ErrInvalidToolRound},
		{name: "false complete", declarations: map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundComplete}, want: contexty.ErrToolRoundStateConflict},
		{name: "unknown state", declarations: map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundState("unknown")}, want: contexty.ErrUnknownToolRoundState},
		{name: "unknown assistant", declarations: map[string]contexty.ToolRoundState{"absent": contexty.ToolRoundInterrupted}, want: contexty.ErrUnknownToolRoundState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			messages := fixtureRoundMessages()
			if tc.mutate != nil {
				messages = tc.mutate(messages)
			}
			// Act.
			observations, err := contexty.InspectToolRoundStates(messages, tc.declarations)
			// Assert: no partial report from malformed history.
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, observations)
		})
	}
}

func TestRound_StateMissingOrderAndScopes(t *testing.T) {
	// Arrange: reused call IDs are scoped to different assistant messages.
	messages := fixtureRoundMessages()[:1]
	second := messages[0].Clone()
	second.ID = "another"
	messages = append(messages, second)
	// Act.
	observations, err := contexty.InspectToolRoundStates(
		messages,
		map[string]contexty.ToolRoundState{"another": contexty.ToolRoundInterrupted},
	)
	// Assert: declarations never affect another round with the same call IDs.
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, observations[0].MissingCallIDs)
	require.Equal(t, contexty.ToolRoundPending, observations[0].State)
	require.Equal(t, contexty.ToolRoundInterrupted, observations[1].State)
	observations[0].MissingCallIDs[0] = "changed"
	require.Equal(t, "first", observations[1].MissingCallIDs[0])
	require.Equal(t, "first", messages[0].ToolCallParts()[0].ID)
}
