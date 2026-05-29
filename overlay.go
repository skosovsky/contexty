package contexty

import (
	"context"
	"maps"
)

type compileOverlayKey struct{}

// WithCompileOverlay attaches ephemeral overlay vars to ctx for DeferredBlock.Resolve.
func WithCompileOverlay(ctx context.Context, overlay Overlay) context.Context {
	if len(overlay) == 0 {
		return ctx
	}
	cp := make(Overlay, len(overlay))
	maps.Copy(cp, overlay)
	return context.WithValue(ctx, compileOverlayKey{}, cp)
}

// CompileOverlayFromContext returns overlay vars injected during Compile.
func CompileOverlayFromContext(ctx context.Context) Overlay {
	if ctx == nil {
		return nil
	}
	ov, _ := ctx.Value(compileOverlayKey{}).(Overlay)
	return ov
}
