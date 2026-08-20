package httpapi

import (
	"context"

	"github.com/novkostya/quince/core/internal/auth"
)

// principalCtxKey is the request-context key for the authenticated principal.
//
// AN UNEXPORTED STRUCT TYPE, which is the standard way to make a context key uncollidable: no other
// package can construct this type, so nothing outside `httpapi` can overwrite what `authGuard` put
// there. That matters more here than for an ordinary value — this is the thing capability checks
// will read, and a key another package could write to would be a way to forge one.
type principalCtxKey struct{}

// withPrincipal attaches the principal to a request context. Called by `authGuard` and nothing else.
func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the principal bound by `authGuard`, and whether there was one.
//
// THE BOOL IS NOT DECORATION AND MUST NOT BE DROPPED. The exempt routes in `authExempt` run with no
// principal at all — that is what "exempt" means — so a caller that ignores the second value gets a
// zero `Principal`, which reads as a password login, which reads as the admin. A missing principal
// silently becoming the admin is the exact failure shape spec D6 is about, arriving through the
// accessor rather than through a credential predicate.
//
// So: an unauthenticated request has NO principal. It does not have an admin one.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(auth.Principal)
	return p, ok
}
