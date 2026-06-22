package contexty_test

import (
	"strconv"

	"github.com/skosovsky/contexty"
)

func fixtureRecordProfile(names ...string) contexty.RecordProfile {
	targets := make(map[string]contexty.Descriptor)
	for _, name := range names {
		targets[name] = contexty.Descriptor{ID: "target-" + name, Revision: "pinned"}
	}
	return contexty.RecordProfile{
		Pipeline:  contexty.Descriptor{ID: "pipeline", Revision: "pinned"},
		Model:     contexty.Descriptor{ID: "model", Revision: "pinned"},
		Prompt:    contexty.Descriptor{ID: "prompt", Revision: "pinned"},
		Estimator: contexty.Descriptor{ID: "estimator", Revision: "pinned"},
		Rendering: contexty.Descriptor{ID: "rendering", Revision: "pinned"}, Targets: targets,
	}
}

func fixtureBindings(profile contexty.RecordProfile, bindings ...contexty.RecordingComponent) contexty.RecordProfile {
	profile.Components = append(profile.Components, bindings...)
	return profile
}

func fixtureBinding(
	kind contexty.RecordingComponentKind,
	target string,
	segment contexty.SegmentName,
	index int,
) contexty.RecordingComponent {
	return contexty.RecordingComponent{
		Key: contexty.RecordingComponentKey{Kind: kind, Target: target, Segment: segment, Index: index},
		Descriptor: contexty.Descriptor{
			ID:       string(kind) + "/" + target + "/" + string(segment) + "/" + strconv.Itoa(index),
			Revision: "pinned",
		},
	}
}
