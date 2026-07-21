package authn

import "context"

// Method records how a request was authenticated.
type Method string

const (
	MethodJWT Method = "jwt"
	MethodPAT Method = "pat"
)

// Principal is the authenticated identity attached to a request. It is
// produced by the auth module and consumed by every other module (RBAC
// checks, audit actor attribution) without cross-module imports.
type Principal struct {
	UserID    string
	Username  string
	SessionID string
	Method    Method
	IsAdmin   bool
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// RequestMeta carries transport facts (for audit) through context.
type RequestMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

type metaKey struct{}

func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

func RequestMetaFromContext(ctx context.Context) RequestMeta {
	m, _ := ctx.Value(metaKey{}).(RequestMeta)
	return m
}
