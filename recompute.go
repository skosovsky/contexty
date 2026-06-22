package contexty

import (
	"context"
	"errors"
)

var ErrRecomputeIdentity = errors.New("contexty: recompute requires a new compilation identity")

func cloneContentRef(ref *ContentRef) *ContentRef {
	if ref == nil {
		return nil
	}
	copyRef := *ref
	return &copyRef
}

// Recompute executes the current engine on explicitly supplied content. It never
// fetches raw inputs from the previous manifest. New identity and lineage are
// mandatory; inherited graph metadata is retained unless caller provides a graph.
func (e *Engine) Recompute(
	ctx context.Context,
	previous CompileManifest,
	request CompileRequest,
) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, err
	}
	if err := previous.Validate(); err != nil {
		return CompileResult{}, err
	}
	if e.recording == nil || e.trace == nil {
		return CompileResult{}, ErrUnsupportedReplay
	}
	if request.CompilationID == "" || request.CompilationID == previous.ID {
		return CompileResult{}, ErrRecomputeIdentity
	}
	request.PreviousRecord = &ContentRef{ID: previous.ID, Digest: previous.Digest, Occurrence: ""}
	if len(request.Lineage.Records) == 0 && len(request.Lineage.Unresolved) == 0 {
		for _, output := range previous.Outputs {
			if output.Kind == ManifestMainOutput {
				request.Lineage = output.Lineage.Clone()
				break
			}
		}
	}
	return e.CompileSnapshot(ctx, request)
}
