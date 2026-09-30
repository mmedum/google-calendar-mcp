package service

import (
	"context"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v2/internal/render"
)

// Asker puts a question to the person using the server before a write
// that cannot be undone, that widens who can see a calendar, or that
// emails guests a cancellation (§9a). Ask returns nil when the write may
// go ahead, and an error to return in its place otherwise; the tools
// layer installs one per call, for the tools that ask.
type Asker interface {
	Ask(ctx context.Context, q render.Question) error
}

type askerKey struct{}

// WithAsker returns a context whose asking writes are put to a.
func WithAsker(ctx context.Context, a Asker) context.Context {
	return context.WithValue(ctx, askerKey{}, a)
}

// ask is the last step before an asking write, after every read, every
// other guard and the dry run, so the question shows what the write
// would do and nothing is asked that a guard would refuse anyway. A
// write reached with no asker is refused: only a tool registered to ask
// may make one.
func ask(ctx context.Context, q render.Question) error {
	a, ok := ctx.Value(askerKey{}).(Asker)
	if !ok {
		return gapi.Errf(gapi.ClassBlocked, "this write has no way to ask the person, which is a defect in "+
			"this server; nothing was changed")
	}
	return a.Ask(ctx, q)
}
