package evaluation

import (
	"maps"
	"slices"

	"github.com/skosovsky/contexty"
)

func cloneFixture(fixture Fixture) Fixture {
	fixture.Messages = cloneMessages(fixture.Messages)
	fixture.RequiredIDs = slices.Clone(fixture.RequiredIDs)
	fixture.QuerySourceIDs = slices.Clone(fixture.QuerySourceIDs)
	fixture.ExpectedFacts = maps.Clone(fixture.ExpectedFacts)
	fixture.FactVersions = maps.Clone(fixture.FactVersions)
	fixture.ExpectedSources = maps.Clone(fixture.ExpectedSources)
	for key, sources := range fixture.ExpectedSources {
		fixture.ExpectedSources[key] = slices.Clone(sources)
	}
	fixture.ForbiddenInstructions = slices.Clone(fixture.ForbiddenInstructions)
	return fixture
}

func cloneFixtures(fixtures []Fixture) []Fixture {
	out := make([]Fixture, len(fixtures))
	for i, fixture := range fixtures {
		out[i] = cloneFixture(fixture)
	}
	return out
}

func cloneModelResult(result ModelResult) ModelResult {
	result.Usage = cloneMeasurement(result.Usage)
	result.Cost = cloneMeasurement(result.Cost)
	return result
}

func cloneMeasurement(value Measurement) Measurement {
	if value.Value != nil {
		copied := *value.Value
		value.Value = &copied
	}
	return value
}

func cloneProfile(profile contexty.EstimateProfile) contexty.EstimateProfile {
	profile.Capabilities = maps.Clone(profile.Capabilities)
	profile.Extensions = maps.Clone(profile.Extensions)
	if profile.Fallback != nil {
		fallback := *profile.Fallback
		profile.Fallback = &fallback
	}
	return profile
}
