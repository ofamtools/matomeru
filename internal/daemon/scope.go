package daemon

import "context"

// Scope is the authority a caller gives up before its first call: the daemon's
// scope own, which reaches only the caller's own session, the sessions of its
// fan group and the sessions its fans started (restrict-connection). PaneID
// and PaneToken name the caller's pane for a caller the kernel cannot place
// in one; with neither found the restriction fails closed and every
// session-reaching call is refused.
type Scope struct {
	PaneID    string
	PaneToken string
}

type scopeKey struct{}

// WithScope returns a context whose calls and subscriptions first restrict
// their connection to scope own, as Scope says.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// scopeOf is the scope ctx carries, if any.
func scopeOf(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(Scope)
	return s, ok
}

// restrict applies the context's scope to a fresh connection.
func (c *connection) restrict(ctx context.Context) error {
	s, ok := scopeOf(ctx)
	if !ok {
		return nil
	}
	params := map[string]any{"scope": "own"}
	if s.PaneID != "" {
		params["pane_id"] = s.PaneID
	}
	if s.PaneToken != "" {
		params["pane_token"] = s.PaneToken
	}
	return exchange(c, "restrict-connection", params, nil)
}
