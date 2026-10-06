package contexty

import "context"

// compileSession binds private stage dependencies. Bindings are copied when a
// target overrides them; operation-owned collectors are shared deliberately.
// The context bridge propagates bindings through contextual callbacks/helpers.
type compileSession struct {
	Context     context.Context
	Recorder    *transformRecorder
	Resources   *resourceCompileState
	Identity    compileIdentitySettings
	HasIdentity bool
}

type compileSessionKey struct{}

func newCompileSession(ctx context.Context) *compileSession {
	return &compileSession{
		Context:     ctx,
		Recorder:    nil,
		Resources:   nil,
		Identity:    compileIdentitySettings{policy: nil, requireDurable: false, turnID: "", targetName: ""},
		HasIdentity: false,
	}
}

func compileSessionFrom(ctx context.Context) *compileSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(compileSessionKey{}).(*compileSession)
	return session
}

func forkCompileSession(ctx context.Context) *compileSession {
	session := newCompileSession(ctx)
	if parent := compileSessionFrom(ctx); parent != nil {
		*session = *parent
		session.Context = ctx
	}
	return session
}

func (s *compileSession) bind() context.Context {
	s.Context = context.WithValue(s.Context, compileSessionKey{}, s)
	return s.Context
}
