package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestComposition(t *testing.T) {
	// Arrange: deterministic, offline host recipe with no provider/network ports.
	ctx := context.Background()
	// Act.
	report, err := runComposition(ctx)
	// Assert: the preview and summary do not masquerade as the original evidence.
	require.NoError(t, err)
	require.Greater(t, report.OriginalBytes, len(report.Preview))
	require.NotContains(t, report.Preview, "4317")
	require.NotContains(t, report.Summary, "4317")
	require.Contains(t, report.Summary, "do not disclose credentials")
	require.Equal(t, []string{"recent-1", "recent-2"}, report.RecentIDs)
	require.True(t, report.RestoredOriginal)
	require.True(t, report.DeniedRead)
	require.True(t, report.StaleRead)
	require.True(t, report.BoundedRead)
	require.True(t, report.ClaimProtected)
	require.True(t, report.Collected)
	require.Equal(t, 2, report.FactReplacement.AcceptedVersion)
	require.Equal(t, "not measured", report.ProviderUsage)
	require.Contains(t, report.Durability, "ephemeral")
}

func TestHostFactConflictDecision(t *testing.T) {
	// Arrange: the host supplied conflicting revisions and explicit source identities.
	previous := hostFact{Key: "region", Value: "east", Version: 1, Sources: []contexty.SourceRef{{ID: "region-v1"}}}
	next := hostFact{Key: "region", Value: "west", Version: 2, Sources: []contexty.SourceRef{{ID: "region-v2"}}}
	// Act.
	accepted, decision, err := replaceFact(previous, next)
	// Assert: replacement is explicit and versioned; no text-based truth inference.
	require.NoError(t, err)
	require.Equal(t, "west", accepted.Value)
	require.Equal(t, next.Sources, accepted.Sources)
	require.Equal(t, 1, decision.PreviousVersion)
	require.Equal(t, 2, decision.AcceptedVersion)
	// Arrange/Act/Assert: stale or unsourced revisions cannot silently replace facts.
	next.Version = 1
	_, _, err = replaceFact(previous, next)
	require.Error(t, err)
	next.Version = 2
	next.Sources = nil
	_, _, err = replaceFact(previous, next)
	require.Error(t, err)
}
